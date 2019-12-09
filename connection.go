package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"time"

	"mphttp/dep/http2"
)

type MpConn interface {
	MeasureRtt() time.Duration
	Close()
	StartRequest(r *http.Request) responseStream
}

type MonitoredMpConn struct {
	conn MpConn
	mon  RttMonitor
}

type responseStream struct {
	response *http.Response
	stream   *http2.ClientStream
}

type mpConn struct {
	clientConn *http2.ClientConn
	tlsConn    *tls.Conn
	keylogFile *os.File
}

func NewMpConn(server string) MpConn {
	file, err := os.OpenFile("keylog.txt", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	fatal("keylog", err)
	tr := &http2.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			KeyLogWriter:       file,
		},
	}
	conn, err := tls.Dial("tcp", server, tr.TLSClientConfig)
	fatal("tls dial to "+server, err)
	clientConn, err := tr.NewClientConn(conn)
	fatal("http2 conn", err)
	return &mpConn{
		keylogFile: file,
		tlsConn:    conn,
		clientConn: clientConn,
	}
}

func NewMonitoredMpConn(server string) MonitoredMpConn {
	conn := NewMpConn(server)
	mon := NewRttMonitor(conn)
	mon.Start()
	return MonitoredMpConn{
		conn: conn,
		mon:  mon,
	}
}

func (c *mpConn) StartRequest(r *http.Request) responseStream {
	resp, cs, err := c.clientConn.RoundTrip(r)
	if err != nil {
		log.Print("response in StartRequest: ", err)
		return responseStream{}
	}
	return responseStream{
		response: resp,
		stream:   cs,
	}
}

func (c *mpConn) MeasureRtt() time.Duration {
	start := time.Now()
	err := c.clientConn.Ping(context.Background())
	if err != nil {
		//log.Print("ping: ", err)
		return 0
	}
	return time.Since(start)
}

func (c *mpConn) Close() {
	c.clientConn.Close()
	c.tlsConn.Close()
	c.keylogFile.Close()
}

func (c MonitoredMpConn) StartRequest(r *http.Request) responseStream {
	return c.conn.StartRequest(r)
}

func (c MonitoredMpConn) MeasureRtt() time.Duration {
	return c.mon.GetRtt()
}

func (c MonitoredMpConn) Close() {
	c.conn.Close()
}
