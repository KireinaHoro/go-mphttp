package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/alexflint/go-arg"
	"os"
	"strings"
	"time"
)

const (
	serverCount    = 3
	requestTimeout = 5 * time.Second
	plotFile       = "plot.gnu"
	plotWidth      = 800
	plotHeight     = 600
)

var args struct {
	Path        string   `arg:"-t" arg:"required" help:"the absolute file path on the CDN server" placeholder:"<file>"`
	OutFilename string   `arg:"-o" arg:"required" help:"save the download to <file>" placeholder:"<file>"`
	Servers     []string `arg:"positional" arg:"required"`
}

func main() {
	p := arg.MustParse(&args)
	if len(args.Servers) != 3 {
		p.Fail("must provide exactly 3 servers")
	}
	// append 443 (https port) to unspecified servers
	for i := range args.Servers {
		if strings.Index(args.Servers[i], ":") < 0 {
			args.Servers[i] += ":443"
		}
	}

	// open output file, exit on failure
	outFile, err := os.OpenFile(args.OutFilename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	fatal("open file", err)
	defer outFile.Close()

	// start all 3 connections
	// range: bytes=0- for Content-Range in response
	globalStart = time.Now()
	fullReq := LeftRangedGet(args.Path, 0)
	connCh := make(chan MonitoredMpConn, serverCount)
	respCh := make(chan responseStream, serverCount)
	for i := 0; i < serverCount; i++ {
		go func(i int) {
			conn := NewMonitoredMpConn(args.Servers[i])
			// fill response first so conn and resp are in the same order
			respCh <- conn.StartRequest(fullReq)
			connCh <- conn
		}(i)
	}

	resps, conns := make([]responseStream, serverCount), make([]MonitoredMpConn, serverCount)
	connsReady := make([]chan struct{}, serverCount)
	for idx := range connsReady {
		connsReady[idx] = make(chan struct{})
	}
	for i := 0; i < serverCount; i++ {
		go func(i int) {
			resps[i], conns[i] = <-respCh, <-connCh
			close(connsReady[i])
		}(i)
	}
	// resps and conns are sorted in order of earlier completion

	<-connsReady[0]
	response := resps[0].response
	length := getTotalLength(response)
	fmt.Printf("Total length: %d\n", length)

	buf := make([]byte, length)
	nSplitRequest(args.Path, conns, connsReady, nil, 0, length, buf, &resps[0])
	duration := time.Since(globalStart)

	fmt.Printf("Download finished, writing output...")
	start := time.Now()
	_, err = outFile.Write(buf)
	fmt.Printf("took %v.\n", time.Since(start))
	fatal("write", err)

	fmt.Printf("%s (sha256 %x) %v\n", args.OutFilename,
		sha256.Sum256(buf), duration)

	for idx := range connDataMap.m {
		connDataMap.m[idx].Close()
		conns[idx].Close()
	}

	fmt.Printf("Writing %s...", plotFile)
	plotF, err := os.OpenFile(plotFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	defer plotF.Close()
	fatal("plot file", err)
	fmt.Fprintf(plotF, `set terminal png transparent enhanced font "arial,10" fontscale 1.0 size %d, %d
set output 'gnuplot.png'
set style increment default
set style data lines
set xlabel 'time elapsed (ms)'
set ylabel 'byte range (Kb)'
plot [0:%d][0:%d] "0.dat" title '0' with points, "1.dat" title '1' with points, "2.dat" title '2' with points`,
		plotWidth, plotHeight, int64(duration / time.Millisecond), length)
	fmt.Printf("...done\n")
}
