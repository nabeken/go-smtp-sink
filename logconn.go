package main

import (
	"io"
	"net"
	"time"
)

// logConn is a net.Conn wrapper that logs all the read and write operations to a writer.
type logConn struct {
	inner net.Conn
	w     io.Writer
}

// Read reads data from the connection.
// It logs the data read and copies it to the provided writer.
func (c *logConn) Read(b []byte) (n int, err error) {
	n, err = c.inner.Read(b)
	_, _ = c.w.Write(b[:n])
	return n, err
}

// Write writes data to the connection.
// It logs the data written and copies it to the provided writer.
func (c *logConn) Write(b []byte) (n int, err error) {
	n, err = c.inner.Write(b)
	_, _ = c.w.Write(b[:n])
	return n, err
}

func (c logConn) Close() error {
	return c.inner.Close()
}

func (c logConn) LocalAddr() net.Addr {
	return c.inner.LocalAddr()
}

func (c logConn) RemoteAddr() net.Addr {
	return c.inner.RemoteAddr()
}

func (c logConn) SetDeadline(t time.Time) error {
	return c.inner.SetDeadline(t)
}

func (c logConn) SetReadDeadline(t time.Time) error {
	return c.inner.SetReadDeadline(t)
}

func (c logConn) SetWriteDeadline(t time.Time) error {
	return c.inner.SetWriteDeadline(t)
}
