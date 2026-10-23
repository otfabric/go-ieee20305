// SPDX-License-Identifier: MIT

package tlsccm

import (
	"context"
	"net"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/tlsccm/internal/gotls"
)

// defaultHandshakeTimeout bounds a handshake when Options.HandshakeTimeout
// is zero.
const defaultHandshakeTimeout = 10 * time.Second

// listener completes the handshake of each connection of a gotls listener
// before Accept returns it, as net/http does for crypto/tls connections:
// net/http does not know gotls connections, and would otherwise run their
// handshakes on its own goroutine without a bound, and without logging
// their failures.
//
// A temporary Accept error goes to the caller, which net/http retries.
// Close cancels the handshakes in flight, closes the connections Accept
// has not returned, and returns once every goroutine of the listener has
// ended.
type listener struct {
	net.Listener
	log     ieee20305.Logger
	timeout time.Duration

	conns chan net.Conn
	errs  chan error // temporary Accept errors, for the caller to retry

	// stopped is closed when acceptLoop returns; acceptErr is set before.
	stopped   chan struct{}
	acceptErr error

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newListener(inner net.Listener, log ieee20305.Logger, timeout time.Duration) *listener {
	ctx, cancel := context.WithCancel(context.Background())
	l := &listener{
		Listener: inner,
		log:      log,
		timeout:  timeout,
		conns:    make(chan net.Conn),
		errs:     make(chan error),
		stopped:  make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
	l.wg.Add(1)
	go l.acceptLoop()
	return l
}

func (l *listener) acceptLoop() {
	defer l.wg.Done()
	defer close(l.stopped)
	for {
		c, err := l.Listener.Accept()
		if err == nil {
			l.wg.Add(1)
			go l.handshake(c)
			continue
		}
		if l.ctx.Err() == nil && temporary(err) {
			select {
			case l.errs <- err:
				continue
			case <-l.ctx.Done():
			}
		}
		if l.ctx.Err() != nil {
			err = net.ErrClosed
		}
		l.acceptErr = err
		return
	}
}

// temporary matches the errors the Serve loop of net/http retries, such as
// EMFILE, with the same check it uses.
func temporary(err error) bool {
	te, ok := err.(interface{ Temporary() bool })
	return ok && te.Temporary()
}

func (l *listener) handshake(c net.Conn) {
	defer l.wg.Done()
	if gc, ok := c.(*gotls.Conn); ok {
		ctx, cancel := context.WithTimeout(l.ctx, l.timeout)
		err := gc.HandshakeContext(ctx)
		cancel()
		if err != nil {
			// A handshake that Close cut short is not the peer's failure.
			if l.ctx.Err() == nil {
				l.log.Warnf("TLS handshake error from %s: %v", c.RemoteAddr(), err)
			}
			_ = c.Close()
			return
		}
	}
	select {
	case l.conns <- c:
	case <-l.ctx.Done():
		_ = c.Close()
	}
}

// Accept returns the next connection whose handshake succeeded.
func (l *listener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		// Both cases may be ready after Close: hand out no connection
		// once the listener is closed.
		select {
		case <-l.ctx.Done():
			_ = c.Close()
			return nil, net.ErrClosed
		default:
			return c, nil
		}
	case err := <-l.errs:
		return nil, err
	case <-l.stopped:
		return nil, l.acceptErr
	}
}

// Close stops accepting, cancels the handshakes in flight, and waits for
// the goroutines of the listener.
func (l *listener) Close() error {
	l.cancel()
	err := l.Listener.Close()
	l.wg.Wait()
	return err
}
