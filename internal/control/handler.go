package control

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"strings"
	"sync"

	"github.com/idreamshen/anylan/internal/protocol"
	"github.com/idreamshen/anylan/internal/relay"
	"github.com/quic-go/quic-go"
)

var errRejected = errors.New("join rejected")

type Handler struct {
	Manager *relay.Manager
	MTU     int
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
	if err := h.handleStream(ctx, stream); errors.Is(err, errRejected) {
		closeConn = false
	}
}

func (h Handler) handleStream(ctx context.Context, stream quic.Stream) error {
	var join protocol.JoinRoom
	if err := protocol.ReadJSON(stream, protocol.TypeJoinRoom, protocol.MaxControlSize, &join); err != nil {
		rejectAndCloseStream(stream, "invalid join request")
		return errRejected
	}
	join.Room = strings.TrimSpace(join.Room)
	if join.Version != protocol.Version {
		rejectAndCloseStream(stream, "unsupported protocol version")
		return errRejected
	}
	if join.Room == "" {
		rejectAndCloseStream(stream, "room is required")
		return errRejected
	}

	peer, err := h.Manager.Join(join.Room, relay.JoinOptions{
		DisplayName: join.DisplayName,
	})
	if err != nil {
		rejectAndCloseStream(stream, err.Error())
		return errRejected
	}

	// On exit: remove peer then broadcast the updated peer list to everyone
	// still in the room.
	defer func() {
		peer.Room.RemovePeer(peer)
		msg := buildPeerListMsg(peer.Room.Snapshot())
		peer.Room.BroadcastNotify(msg, nil)
	}()

	// Build initial peer list (includes the joining peer itself).
	roomSnap := peer.Room.Snapshot()
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

	// mu serialises all writes to stream (frame goroutine + notify goroutine +
	// main loop pong replies).
	var mu sync.Mutex
	lockedWrite := func(typ protocol.MessageType, payload []byte) error {
		mu.Lock()
		defer mu.Unlock()
		return protocol.WriteMessage(stream, typ, payload)
	}

	if err := func() error {
		mu.Lock()
		defer mu.Unlock()
		return protocol.WriteJSON(stream, protocol.TypeJoinAccept, accept)
	}(); err != nil {
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
			if err := lockedWrite(protocol.TypeEthernetFrame, frame); err != nil {
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
				mu.Lock()
				_, err := stream.Write(msg)
				mu.Unlock()
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

		typ, payload, err := protocol.ReadMessage(stream, protocol.MaxFrameSize)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch typ {
		case protocol.TypeEthernetFrame:
			peer.RecordRxFrame(len(payload))
			targets, err := peer.Room.Forward(peer, payload)
			if err != nil {
				continue
			}
			for _, target := range targets {
				target.Enqueue(payload)
			}
		case protocol.TypePing:
			if err := lockedWrite(protocol.TypePong, payload); err != nil {
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
