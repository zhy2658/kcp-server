package network

import (
	"fmt"
	"io"
	"net"

	"game-server/internal/config"

	"github.com/topfreegames/pitaya/v2/acceptor"
	"github.com/topfreegames/pitaya/v2/conn/codec"
	"github.com/topfreegames/pitaya/v2/logger"
	kcp "github.com/xtaci/kcp-go/v5"
)

type KCPAcceptor struct {
	addr          string
	connChan      chan acceptor.PlayerConn
	listener      *kcp.Listener
	running       bool
	proxyProtocol bool
	cfg           *config.Config
}

type kcpPlayerConn struct {
	net.Conn
}

// GetNextMessage reads the next message available in the stream
func (k *kcpPlayerConn) GetNextMessage() (b []byte, err error) {
	// Add recovery to prevent server crash on panic inside GetNextMessage
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in GetNextMessage: %v", r)
			logger.Log.Errorf("Recovered from panic in KCP GetNextMessage: %v", r)
		}
	}()

	header := make([]byte, codec.HeadLength)
	if _, err := io.ReadFull(k.Conn, header); err != nil {
		return nil, err
	}

	size, _, err := codec.ParseHeader(header)
	if err != nil {
		return nil, err
	}

	msg := make([]byte, size)
	if _, err := io.ReadFull(k.Conn, msg); err != nil {
		return nil, err
	}

	return append(header, msg...), nil
}

// Ensure kcpPlayerConn implements acceptor.PlayerConn
var _ acceptor.PlayerConn = &kcpPlayerConn{}

func NewKCPAcceptor(cfg *config.Config) *KCPAcceptor {
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	return &KCPAcceptor{
		addr:          addr,
		connChan:      make(chan acceptor.PlayerConn),
		running:       false,
		proxyProtocol: false,
		cfg:           cfg,
	}
}

func (a *KCPAcceptor) GetAddr() string {
	if a.listener != nil {
		return a.listener.Addr().String()
	}
	return ""
}

func (a *KCPAcceptor) GetConnChan() chan acceptor.PlayerConn {
	return a.connChan
}

func (a *KCPAcceptor) Stop() {
	a.running = false
	if a.listener != nil {
		a.listener.Close()
	}
}

func (a *KCPAcceptor) IsRunning() bool {
	return a.running
}

func (a *KCPAcceptor) GetConfiguredAddress() string {
	return a.addr
}

func (a *KCPAcceptor) EnableProxyProtocol() {
	a.proxyProtocol = true
}

func (a *KCPAcceptor) ListenAndServe() {
	// Use nil block for no encryption.
	// For production, consider using a block cipher like kcp.NewAESBlockCrypt
	// Disable FEC (Forward Error Correction) for Unity C# Client compatibility
	// (Unless C# client supports FEC, which is complex)
	l, err := kcp.ListenWithOptions(a.addr, nil, 0, 0)
	if err != nil {
		logger.Log.Fatalf("Failed to listen: %s", err.Error())
	}

	a.listener = l
	a.running = true

	defer a.Stop()

	logger.Log.Infof("KCP Acceptor listening on %s", a.addr)

	for a.running {
		conn, err := a.listener.Accept()
		if err != nil {
			logger.Log.Errorf("Failed to accept KCP connection: %s", err.Error())
			continue
		}

		// Configure KCP parameters for low latency
		if kcpConn, ok := conn.(*kcp.UDPSession); ok {
			// NoDelay(nodelay, interval, resend, nc)
			kcpConn.SetNoDelay(
				a.cfg.KCP.NoDelay,
				a.cfg.KCP.Interval,
				a.cfg.KCP.Resend,
				a.cfg.KCP.NC,
			)

			// Pitaya protocol relies on stream processing (Head + Body)
			kcpConn.SetStreamMode(true)

			kcpConn.SetWindowSize(a.cfg.KCP.SndWnd, a.cfg.KCP.RcvWnd)
			kcpConn.SetACKNoDelay(a.cfg.KCP.AckNoDelay)
		}

		a.connChan <- &kcpPlayerConn{Conn: conn}
	}
}
