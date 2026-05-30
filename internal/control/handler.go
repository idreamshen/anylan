package control

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/idreamshen/anylan/internal/capture"
	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/relay"
	"github.com/quic-go/quic-go"
)

var errRejected = errors.New("join rejected")

type Handler struct {
	Manager *relay.Manager
	MTU     int
	Capture *capture.Recorder
}

func (h Handler) HandleConnection(ctx context.Context, conn quic.Connection) {
	closeConn := true
	defer func() {
		if closeConn {
			conn.CloseWithError(0, "")
		}
	}()

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	defer stream.Close()

	if h.MTU == 0 {
		h.MTU = protocol.DefaultMTU
	}
	if err := h.handleStream(ctx, conn, stream); errors.Is(err, errRejected) {
		closeConn = false
	}
}

func (h Handler) handleStream(ctx context.Context, conn quic.Connection, controlStream quic.Stream) (retErr error) {
	remote := conn.RemoteAddr().String()
	var join protocol.JoinRoom
	if err := protocol.ReadJSON(controlStream, protocol.TypeJoinRoom, protocol.MaxControlSize, &join); err != nil {
		logJoinReject(remote, "", "invalid join request", err)
		rejectAndCloseStream(controlStream, "invalid join request")
		return errRejected
	}
	join.Room = strings.TrimSpace(join.Room)
	if join.Version != protocol.Version {
		logJoinReject(remote, join.Room, "unsupported protocol version", nil)
		rejectAndCloseStream(controlStream, "unsupported protocol version")
		return errRejected
	}
	if join.Room == "" {
		logJoinReject(remote, join.Room, "room is required", nil)
		rejectAndCloseStream(controlStream, "room is required")
		return errRejected
	}
	var requestedMAC net.HardwareAddr
	if strings.TrimSpace(join.MAC) != "" {
		var err error
		requestedMAC, err = net.ParseMAC(join.MAC)
		if err != nil {
			logJoinReject(remote, join.Room, "invalid mac address", err)
			rejectAndCloseStream(controlStream, "invalid mac address")
			return errRejected
		}
	}
	peer, err := h.Manager.Join(join.Room, relay.JoinOptions{
		DisplayName:  join.DisplayName,
		RequestedMAC: requestedMAC,
	})
	if err != nil {
		logJoinReject(remote, join.Room, err.Error(), err)
		rejectAndCloseStream(controlStream, err.Error())
		return errRejected
	}

	// On exit: remove peer then broadcast the updated peer list to everyone
	// still in the room.
	defer func() {
		room := peer.Room
		roomName := room.Name()
		peerSnap := peer.Snapshot()
		peer.Room.RemovePeer(peer)
		remaining := room.PeerCount()
		logPeerLeft(remote, roomName, peerSnap, remaining, retErr)
		if remaining > 0 {
			msg := buildPeerListMsg(room.Snapshot())
			room.BroadcastNotify(msg, nil)
		} else {
			log.Printf("room empty room=%q", roomName)
		}
	}()

	// Build initial peer list (includes the joining peer itself).
	roomSnap := peer.Room.Snapshot()
	if len(roomSnap.Peers) == 1 {
		log.Printf("room created room=%q prefix=%s", roomSnap.Name, roomSnap.Prefix)
	}
	log.Printf("peer joined room=%q peer=%s name=%q ip=%s mac=%s remote=%s peers=%d", peer.Room.Name(), peer.ID, peer.DisplayName, peer.IP, peer.MAC, remote, len(roomSnap.Peers))
	accept := protocol.JoinAccept{
		Version:       protocol.Version,
		Room:          join.Room,
		PeerID:        peer.ID,
		IPv4:          peer.IP.String(),
		CIDR:          netip.PrefixFrom(peer.IP, peer.Room.Prefix().Bits()).String(),
		MAC:           peer.MAC.String(),
		MTU:           h.MTU,
		RoomCreatedAt: roomSnap.CreatedAt,
		Peers:         toPeerInfos(roomSnap.Peers),
	}

	// controlMu serialises all writes to the control stream (notify goroutine +
	// main loop pong replies). Ethernet frames use a dedicated data stream.
	var controlMu sync.Mutex
	lockedControlWrite := func(typ protocol.MessageType, payload []byte) error {
		controlMu.Lock()
		defer controlMu.Unlock()
		return protocol.WriteMessage(controlStream, typ, payload)
	}

	if err := func() error {
		controlMu.Lock()
		defer controlMu.Unlock()
		return protocol.WriteJSON(controlStream, protocol.TypeJoinAccept, accept)
	}(); err != nil {
		return err
	}

	dataStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer dataStream.Close()
	if err := protocol.WriteMessage(dataStream, protocol.TypePing, nil); err != nil {
		return err
	}

	// Notify existing peers (everyone except the new joiner) about the updated
	// peer list.
	joinMsg := buildPeerListMsg(roomSnap)
	peer.Room.BroadcastNotify(joinMsg, peer)

	writeErr := make(chan error, 1)

	// Goroutine: forward Ethernet frames from relay to client.
	go func() {
		for frame := range peer.Frames {
			if err := protocol.WriteMessage(dataStream, protocol.TypeEthernetFrame, frame); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()

	// Goroutine: forward control notifications (e.g. TypePeerList) to client.
	go func() {
		for {
			select {
			case msg, ok := <-peer.Notify:
				if !ok {
					return
				}
				controlMu.Lock()
				_, err := controlStream.Write(msg)
				controlMu.Unlock()
				if err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-writeErr:
			return err
		default:
		}

		typ, payload, err := protocol.ReadMessage(dataStream, protocol.MaxFrameSize)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch typ {
		case protocol.TypeEthernetFrame:
			h.Capture.Record(capture.Metadata{Direction: "ingress", Room: peer.Room.Name(), PeerID: peer.ID, PeerName: peer.DisplayName}, payload)
			if !peer.AllowFrame(len(payload)) {
				peer.RecordDropFrame(len(payload))
				continue
			}
			peer.RecordRxFrame(len(payload))
			targets, err := peer.Room.Forward(peer, payload)
			if err != nil {
				peer.RecordDropFrame(len(payload))
				continue
			}
			for _, target := range targets {
				target.Enqueue(payload)
			}
		case protocol.TypePing:
			if err := lockedControlWrite(protocol.TypePong, payload); err != nil {
				return err
			}
		}
	}
}

// toPeerInfos converts relay peer snapshots to protocol peer info structs.
func toPeerInfos(peers []relay.PeerSnapshot) []protocol.PeerInfo {
	out := make([]protocol.PeerInfo, 0, len(peers))
	for _, p := range peers {
		out = append(out, protocol.PeerInfo{
			ID:          p.ID,
			DisplayName: p.DisplayName,
			IPv4:        p.IP,
			MAC:         p.MAC,
		})
	}
	return out
}

// buildPeerListMsg serialises a TypePeerList wire message from a room snapshot.
func buildPeerListMsg(snap relay.RoomSnapshot) []byte {
	pl := protocol.PeerList{
		RoomCreatedAt: snap.CreatedAt,
		Peers:         toPeerInfos(snap.Peers),
	}
	var buf bytes.Buffer
	_ = protocol.WriteJSON(&buf, protocol.TypePeerList, pl)
	return buf.Bytes()
}

func reject(w io.Writer, reason string) error {
	return protocol.WriteJSON(w, protocol.TypeJoinReject, protocol.JoinReject{Reason: reason})
}

func rejectAndCloseStream(stream quic.Stream, reason string) {
	_ = reject(stream, reason)
	_ = stream.Close()
}

func logJoinReject(remote, room, reason string, err error) {
	if err != nil {
		log.Printf("join rejected remote=%s room=%q reason=%q error=%v", remote, room, reason, err)
		return
	}
	log.Printf("join rejected remote=%s room=%q reason=%q", remote, room, reason)
}

func logPeerLeft(remote, room string, peer relay.PeerSnapshot, remaining int, sessionErr error) {
	reason := "closed"
	if sessionErr != nil {
		switch {
		case errors.Is(sessionErr, io.EOF):
			reason = "client closed"
		case errors.Is(sessionErr, context.Canceled):
			reason = "server shutdown"
		default:
			reason = sessionErr.Error()
		}
	}
	duration := time.Since(peer.ConnectedAt).Round(time.Second)
	log.Printf(
		"peer left room=%q peer=%s name=%q ip=%s mac=%s remote=%s duration=%s reason=%q remaining=%d rx_frames=%d rx_bytes=%d tx_frames=%d tx_bytes=%d drop_frames=%d drop_bytes=%d",
		room,
		peer.ID,
		peer.DisplayName,
		peer.IP,
		peer.MAC,
		remote,
		duration,
		reason,
		remaining,
		peer.RxFrames,
		peer.RxBytes,
		peer.TxFrames,
		peer.TxBytes,
		peer.DropFrames,
		peer.DropBytes,
	)
}
