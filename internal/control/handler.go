package control

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"strings"

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
	defer peer.Room.RemovePeer(peer)

	accept := protocol.JoinAccept{
		Version: protocol.Version,
		Room:    join.Room,
		PeerID:  peer.ID,
		IPv4:    peer.IP.String(),
		CIDR:    netip.PrefixFrom(peer.IP, peer.Room.Prefix().Bits()).String(),
		MAC:     peer.MAC.String(),
		MTU:     h.MTU,
	}
	if err := protocol.WriteJSON(stream, protocol.TypeJoinAccept, accept); err != nil {
		return err
	}

	writeErr := make(chan error, 1)
	go func() {
		for frame := range peer.Frames {
			if err := protocol.WriteMessage(stream, protocol.TypeEthernetFrame, frame); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
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
			if err := protocol.WriteMessage(stream, protocol.TypePong, payload); err != nil {
				return err
			}
		}
	}
}

func reject(w io.Writer, reason string) error {
	return protocol.WriteJSON(w, protocol.TypeJoinReject, protocol.JoinReject{Reason: reason})
}

func rejectAndCloseStream(stream quic.Stream, reason string) {
	_ = reject(stream, reason)
	_ = stream.Close()
}
