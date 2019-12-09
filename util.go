package main

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func fatal(msg string, err error) {
	if err != nil {
		log.Fatalf("%s: %v\n", msg, err)
	}
}

func printHeaders(response *http.Response) {
	for k, v := range response.Header {
		for _, vv := range v {
			fmt.Printf("%s: %s\n", k, vv)
		}
	}
}

func getTotalLength(response *http.Response) int {
	//printHeaders(response)
	contentRanges, found := response.Header["Content-Range"]
	if !found {
		log.Fatal("no Content-Range header present")
	}
	if len(contentRanges) != 1 {
		log.Fatal("multiple Content-Range header present")
	}
	slashIdx := strings.Index(contentRanges[0], "/")
	if slashIdx < 0 {
		log.Fatal("no slash (/) in Content-Range header")
	}
	ret, err := strconv.Atoi(contentRanges[0][slashIdx+1:])
	fatal("atoi in getTotalLength", err)
	return ret
}

func min(a, b int64) int64 {
	if a > b {
		return b
	}
	return a
}
