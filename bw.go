package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var connDataMap struct {
	m map[int]*os.File
	mux sync.Mutex
}

type BwCounter struct {
	total       int64
	historyRate []int64
	rateSum     int64
	connId      int
	offset      int
	mux         sync.Mutex // protects all above
}

func (wc *BwCounter) Write(p []byte) (int, error) {
	if wc.offset < 0 {
		panic("BwCounter offset uninitialized when first write happened")
	}
	connDataMap.mux.Lock()
	if connDataMap.m == nil {
		connDataMap.m = make(map[int]*os.File)
	}
	if connDataMap.m[wc.connId] == nil {
		var err error
		connDataMap.m[wc.connId], err = os.OpenFile(fmt.Sprintf("%d.dat", wc.connId),
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		fatal("open connection data file", err)
	}
	connDataMap.mux.Unlock()
	n := len(p)
	wc.mux.Lock()
	wc.total += int64(n)
	//fmt.Println("Connection", wc.connId, wc.total)
	connDataMap.mux.Lock()
	fmt.Fprintf(connDataMap.m[wc.connId], "%d %d\n", time.Since(globalStart)/time.Millisecond,
		wc.total+int64(wc.offset))
	connDataMap.mux.Unlock()
	wc.mux.Unlock()
	return n, nil
}

func (wc *BwCounter) Total() int64 {
	wc.mux.Lock()
	defer wc.mux.Unlock()
	return wc.total
}

func (wc *BwCounter) AddRate(rate int64) {
	wc.mux.Lock()
	defer wc.mux.Unlock()
	if rate < 0 {
		panic("rate < 0 in wc.AddRate")
	}
	if wc.rateSum < 0 {
		panic(fmt.Sprintf("wc.rateSum < 0: %d", wc.rateSum))
	}
	wc.historyRate = append(wc.historyRate, rate)
	wc.rateSum += rate
	if len(wc.historyRate) > maxSampleDepth {
		wc.rateSum -= wc.historyRate[0]
		wc.historyRate = wc.historyRate[1:]
	}
}

func (wc *BwCounter) Rate() int64 {
	wc.mux.Lock()
	defer wc.mux.Unlock()
	if l := len(wc.historyRate); l == 0 {
		return 0
	} else {
		return wc.rateSum / int64(l)
	}
}

// we do not reset rate measurements here: each connection has a counter sharing across requests
func (wc *BwCounter) Reset() {
	wc.mux.Lock()
	defer wc.mux.Unlock()
	wc.total = 0
}

func NewBwCounter(id int) *BwCounter {
	return &BwCounter{
		connId: id,
		offset: -1,
	}
}

// DuplicateBwCounter creates new BwCounter that inherits bandwidth measurements but not the progress
// counters.
// Useful for passing down bandwidth counters to further jobs.
func (old *BwCounter) DuplicateBwCounter(id int) (ret *BwCounter) {
	old.mux.Lock()
	defer old.mux.Unlock()
	ret = &BwCounter{
		historyRate: make([]int64, len(old.historyRate)),
		rateSum:     old.rateSum,
		offset:      -1,
		connId:      id,
	}
	copy(ret.historyRate, old.historyRate)
	return
}

func (wc *BwCounter) SetOffset(offset int) {
	wc.mux.Lock()
	defer wc.mux.Unlock()
	wc.offset = offset
}
