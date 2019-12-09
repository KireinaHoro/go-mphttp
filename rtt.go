package main

import (
	"sync"
	"time"
)

const (
	maxSampleDepth = 5
)

type RttMonitor interface {
	Start()
	GetRtt() time.Duration
}

type rttMonitor struct {
	conn       MpConn
	historyRtt chan time.Duration
	rttSum     time.Duration
	mux        sync.Mutex
}

func (r *rttMonitor) Start() {
	ready := make(chan struct{})
	go func() {
		first := true
		for {
			var currentRtt time.Duration
			currentRtt = r.conn.MeasureRtt()
			if currentRtt == 0 {
				if first {
					for ; currentRtt == 0; {
						//fmt.Printf("Remeasuring due to zero rtt on first try\n")
						currentRtt = r.conn.MeasureRtt()
					}
				} else {
					//fmt.Printf("Skipping update due to zero rtt sampled\n")
					goto out
				}
			}
			r.historyRtt <- currentRtt
			r.mux.Lock()
			r.rttSum += currentRtt
			if len(r.historyRtt) > maxSampleDepth {
				r.rttSum -= <-r.historyRtt
			}
			r.mux.Unlock()
		out:
			if first {
				//fmt.Printf("first round rtt: %v\n", currentRtt)
				ready <- struct{}{}
				first = false
			}
			//fmt.Printf("rtt for this round: %v\n", r.getRtt())
			time.Sleep(100 * time.Millisecond)
		}
	}()
	<-ready
}

func (r *rttMonitor) getRtt() time.Duration {
	return time.Duration(int64(r.rttSum/time.Microsecond)/int64(len(r.historyRtt))) * time.Microsecond
}

func (r *rttMonitor) GetRtt() time.Duration {
	r.mux.Lock()
	defer r.mux.Unlock()
	return r.getRtt()
}

func NewRttMonitor(conn MpConn) RttMonitor {
	return &rttMonitor{
		conn:       conn,
		historyRtt: make(chan time.Duration, maxSampleDepth+1),
		rttSum:     0,
		mux:        sync.Mutex{},
	}
}
