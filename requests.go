package main

import (
	"context"
	"fmt"
	"net/http"
)

func UnrangedGet(url string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	fatal("gen request", err)
	return req
}

// cancelling a request will result in RST_STREAM sent on the stream
// the RST_STREAM will arrive one RTT slower, during which the server still writes data
// which wastes bandwidth; control window sizes first
func UnrangedGetWithCancel(url string) (*http.Request, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	fatal("gen request", err)
	return req, cancel
}

func DoubleRangedGet(url string, start, end int) *http.Request {
	req := UnrangedGet(url)
	req.Header.Add("range", fmt.Sprintf("bytes=%d-%d", start, end))
	return req
}

func DoubleRangedGetWithCancel(url string, start, end int) (*http.Request, func()) {
	req, cancel := UnrangedGetWithCancel(url)
	req.Header.Add("range", fmt.Sprintf("bytes=%d-%d", start, end))
	return req, cancel
}

func LeftRangedGet(url string, start int) *http.Request {
	req := UnrangedGet(url)
	req.Header.Add("range", fmt.Sprintf("bytes=%d-", start))
	return req
}

func LeftRangedGetWithCancel(url string, start int) (*http.Request, func()) {
	req, cancel := UnrangedGetWithCancel(url)
	req.Header.Add("range", fmt.Sprintf("bytes=%d-", start))
	return req, cancel
}
