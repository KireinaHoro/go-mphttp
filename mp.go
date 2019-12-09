package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	minSplitSize = 2 << 10
	// bwSampleInterval controls the interval of bandwidth estimation
	// also the interval of graphing data output
	bwSampleInterval = 10 * time.Millisecond
)

// marks the time since download start; used for graphing
var globalStart time.Time

// if firstResponse != nil, that response will be used as the response for the first connection
// at startup phase a response for the full content will be started to fetch length (we can save
// 1 RTT by using GET instead of HEAD).  Pass that response as firstResponse.
// if bw == nil, we do not have bandwidth data yet, so split equally
func nSplitRequest(url string, conns []MonitoredMpConn, bw []*BwCounter, start int, end int, buf []byte,
	firstResponse *responseStream) {
	if start > end {
		log.Panicf("nSplitRequest start=%d end=%d", start, end)
	}
	type taggedBuf struct {
		idx int
		buf []byte
	}
	type taggedResponseStream struct {
		idx int
		rs  responseStream
	}
	type contentRange struct {
		start, end int
	}
	nConns := len(conns)
	//fmt.Printf("start=%d end=%d\n", start, end)
	if end-start < minSplitSize {
		//fmt.Printf("Range %d-%d too short, stop splitting\n", start, end)
		// issue on all connections, see who finishes first
		bufChan := make(chan taggedBuf)
		resps := make([]*http.Response, nConns)
		for idx, conn := range conns {
			go func(idx int) {
				req := DoubleRangedGet(url, start, end)
				if idx == 0 && firstResponse != nil {
					resps[idx] = firstResponse.response
				} else {
					resps[idx] = conn.StartRequest(req).response
				}
				buf := make([]byte, resps[idx].ContentLength)
				_, err := io.ReadFull(resps[idx].Body, buf)
				if err != nil {
					//log.Printf("request on connection %d failed: %v\n", idx, err)
					return
				}
				bufChan <- taggedBuf{
					idx: idx,
					buf: buf,
				}
			}(idx)
		}
		firstFinish := <-bufChan
		// close all other slow connections
		for i := 0; i < nConns; i++ {
			if i != firstFinish.idx {
				if resps[i] != nil {
					resps[i].Body.Close()
				}
			}
		}
		copy(buf[start:end], firstFinish.buf)
		return
	}

	ranges := make([]contentRange, nConns)
	if bw == nil {
		splitSize := (end - start) / nConns
		currStart := start
		for idx := range ranges {
			ranges[idx].start = currStart
			ranges[idx].end = currStart + splitSize
			currStart += splitSize
		}
		// handle non-divisible case
		if r := (end - start) % nConns; r != 0 {
			ranges[len(ranges)-1].end += r
		}
	} else {
		// split according to scheduling algorithm
		var totalBw int64
		singleSample := make([]int64, nConns)
		for i, b := range bw {
			singleSample[i] = b.Rate()
			totalBw += singleSample[i]
		}
		tot := end - start
		for i := range singleSample {
			singleSample[i] = int64(float32(tot) * (float32(singleSample[i]) / float32(totalBw)))
		}
		var currSum int64
		for _, d := range singleSample {
			currSum += d
		}
		singleSample[0] += int64(end - start) - currSum

		currStart := start
		for idx := range ranges {
			ranges[idx].start = currStart
			ranges[idx].end = currStart + int(singleSample[idx])
			currStart += int(singleSample[idx])
		}
	}
	fmt.Println(ranges)

	readyResps := make(chan taggedResponseStream)
	for idx, conn := range conns {
		if idx == 0 && firstResponse != nil {
			// choke the existing request
			//bytes := ranges[idx].end - ranges[idx].start
			//firstResponse.stream.ChokeAt(int64(bytes))
			go func() {
				readyResps <- taggedResponseStream{
					idx: 0,
					rs:  *firstResponse,
				}
			}()
		} else {
			// start a new request
			req := DoubleRangedGet(url, ranges[idx].start, ranges[idx].end)
			go func(idx int, conn MonitoredMpConn) {
				readyResps <- taggedResponseStream{
					idx: idx,
					rs:  conn.StartRequest(req),
				}
			}(idx, conn)
		}
	}

	if bw == nil {
		bw = make([]*BwCounter, nConns)
	}
	for i := range bw {
		if bw[i] == nil {
			bw[i] = NewBwCounter(i)
		}
		bw[i].SetOffset(ranges[i].start)
	}

	// read responses from connections
	rsPerConn := make([]responseStream, nConns)
	rsWg := sync.WaitGroup{}       // done when all streams are ready (and measurements up)
	transferWg := sync.WaitGroup{} // done when all bodies have been read
	for range conns {
		transferWg.Add(1)
		rsWg.Add(1)
		go func() {
			trs := <-readyResps
			rsPerConn[trs.idx] = trs.rs
			resp := trs.rs.response
			if resp.StatusCode != 200 && resp.StatusCode != 206 {
				log.Panicf("unexpected status code from server: %d", resp.StatusCode)
			}
			r := ranges[trs.idx]
			counter := bw[trs.idx]
			countedBody := io.TeeReader(resp.Body, counter)

			// bandwidth sampling & byte counting goroutine - counter.Rate
			go func() {
				var lastBytes int64
				for {
					tot := counter.Total()
					if tot <= lastBytes {
						// bw counter has just reset; wait for a while
						lastBytes = 0
						continue
					}
					delta := tot - lastBytes
					counter.AddRate(delta * int64(time.Second/bwSampleInterval)) // in bytes/s
					lastBytes = tot

					// check on the choked connection: if we're at the desired end then close manually
					// as the remote would be waiting due to unfinished request
					if firstResponse != nil && trs.idx == 0 {
						if int(tot) == r.end-r.start {
							trs.rs.response.Body.Close()
						}
					}
					if int(tot) == r.end-r.start {
						return
					}
					time.Sleep(bwSampleInterval)
				}
			}()
			rsWg.Done()

			//fmt.Println("Reading for", r.start)
			_, err := io.ReadFull(countedBody, buf[r.start:r.end])
			//fmt.Println("Reading for", r.start, "done")
			if err != nil &&
				err.Error() == "net/http: server replied with more than declared Content-Length; truncated" {
				//fmt.Printf("Connection #%d choked and closed\n", trs.idx)
			} else {
				fatal(fmt.Sprintf("unknown error for read body on connection #%d", trs.idx), err)
			}
			transferWg.Done()
		}()
	}

	inflightBytes := func(idx int) int64 {
		rate := bw[idx].Rate()
		rtt := conns[idx].mon.GetRtt()
		//fmt.Printf("#%d: rate %d, rtt %v\n", idx, rate, rtt)
		if rate == 0 || rtt == 0 {
			return 0
		} else {
			return int64(float32(rate) / (float32(time.Second)/float32(rtt)))
		}
	}

	// done marks ready of fragRanges - the near-completion (remaining < 1*inflight) of
	// one connection
	var fragRanges []contentRange
	done := make(chan struct{})

	// wait for all streams
	rsWg.Wait()
	// check for first finishing connection and choke others
	go func(ff *[]contentRange) {
	outer:
		for {
			for idx := range bw {
				prog := bw[idx].Total()
				tot := ranges[idx].end - ranges[idx].start
				inflight := inflightBytes(idx)
				if int64(tot)-prog < inflight {
					// the connection is finishing, choke other connections
					for i := range conns {
						if i != idx {
							bwTotal := bw[i].Total()
							inflight := inflightBytes(i)
							rangeLen := ranges[i].end - ranges[i].start
							chokeAt := min(bwTotal+inflight, int64(rangeLen))
							var newStart, newEnd int
							// we do not need to do anything if the connection will finish in an RTT
							if chokeAt != int64(ranges[i].end - ranges[i].start) {
								if inflight != 0 {
									//fmt.Printf("Choking %v to %d (bwTotal=%d inflight=%d)\n",
									//	ranges[i], chokeAt, bwTotal, inflight)
									// ChokeAt will cut cs.bytesRemain so that the stream ends early
									rsPerConn[i].stream.ChokeAt(chokeAt)
								}
								// do not choke if we failed to figure out inflight bytes
							}
							newStart = ranges[i].start + int(chokeAt)
							newEnd = ranges[i].end
							if newStart < newEnd {
								//fmt.Printf("newStart=%d rstart=%d chokeAt=%d newEnd=%d\n", newStart,
								//	ranges[i].start, int(chokeAt), newEnd)
								*ff = append(*ff, contentRange{
									start: newStart,
									end:   newEnd,
								})
							}
						}
					}
					break outer
				}
			}
			time.Sleep(bwSampleInterval)
		}
		close(done)
	}(&fragRanges)

	<-done

	// create new progress counters that retain bandwidth data
	newBwCounters := make([]*BwCounter, len(bw))
	for idx := range bw {
		//fmt.Printf("Resetting progress counter for connection #%d\n", idx)
		newBwCounters[idx] = bw[idx].DuplicateBwCounter(idx)
	}

	//fmt.Printf("fragRanges: %v\n", fragRanges)
	for _, frag := range fragRanges {
		//fmt.Printf("Restarting for %d-%d\n", frag.start, frag.end)
		nSplitRequest(url, conns, newBwCounters, frag.start, frag.end, buf, nil)
	}

	// do not return until all transfers are ready.
	transferWg.Wait()
}
