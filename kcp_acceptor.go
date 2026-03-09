package main

import (
	"io"
	"net"

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
}

type kcpPlayerConn struct {
	net.Conn
}

// GetNextMessage reads the next message available in the stream
func (k *kcpPlayerConn) GetNextMessage() (b []byte, err error) {
	// 1. Read header (HeadLength is usually 4 bytes)
	header := make([]byte, codec.HeadLength)
	// Use ReadFull to ensure we get the full header
	if _, err := io.ReadFull(k.Conn, header); err != nil {
		return nil, err
	}

	// 2. Parse header to get message size
	msgSize, _, err := codec.ParseHeader(header)
	if err != nil {
		return nil, err
	}

	// 3. Read message body
	msgData := make([]byte, msgSize)
	if _, err := io.ReadFull(k.Conn, msgData); err != nil {
		return nil, err
	}

	// 4. Return combined data
	return append(header, msgData...), nil
}

// Ensure kcpPlayerConn implements acceptor.PlayerConn
var _ acceptor.PlayerConn = &kcpPlayerConn{}

func NewKCPAcceptor(addr string) *KCPAcceptor {
	return &KCPAcceptor{
		addr:          addr,
		connChan:      make(chan acceptor.PlayerConn),
		running:       false,
		proxyProtocol: false,
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
	l, err := kcp.ListenWithOptions(a.addr, nil, 10, 3)
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
			// nodelay: 0:disable(default), 1:enable
			// interval: internal update timer interval in millisec, default 10ms
			// resend: 0:disable fast resend(default), 1:enable fast resend
			// nc: 0:normal congestion control(default), 1:disable congestion control
			kcpConn.SetNoDelay(1, 10, 2, 1)

			// Pitaya protocol relies on stream processing (Head + Body)
			kcpConn.SetStreamMode(true)

			kcpConn.SetWindowSize(128, 128)
			kcpConn.SetACKNoDelay(true)
		}

		a.connChan <- &kcpPlayerConn{Conn: conn}
	}
}
