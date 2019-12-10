# Writing Task for Lab 3: Multipath HTTP/2

## Implementation Overview

This implementation is based on Go 1.13's `x/net/http2` library.  The modified version of the package can be found in `dep/http2` in the source directory.  The following changes are made:

- Expose `x/net/http2`'s `clientStream` struct to users for fine-grain operation.
  - The stream struct is returned on call to `http2.ClientConn.RoundTrip`, which returns a response header.
- Implement an operation named `ChokeAt` on the client streams: to gracefully stop a stream when given number of bytes have been transferred (since start of stream).
  - `ChokeAt` specifies new expected ending of a given stream (opposed to the original `ContentLength` ending) so that the `io.ReadCloser` for `http.Response.Body` will finish automatically.
  - `ChokeAt`, when called, disables window updating for that stream.  Instead, it calculates how many tokens the remote peer needs to finish to the specified choke location and sends them in a single `WINDOW_UPDATE` frame.
    - The server will stop and timeout, sending `GOAWAY` to properly terminate the stream.

The application logic is implemented in a concurrent (goroutines) manner, which, when possible, performs all actions asynchronously so there are no long blocking.  The following sections answer the questions in the lab handout material.

## How do you estimate the bandwidth and delay of a path?

- A goroutine constantly sends `PING` frames and expects answer.  Subsequent `PING` roundtrips are separated 10ms apart.
- The `http.Response.Body` is wrapped by `io.TeeWriter` for counting bytes that were read from the body so far.  Valid readings are taken every 10ms.
- The sum of the bandwidth and RTT measurements are kept with maximum sample depth of 5.  The history average is taken as the current bandwidth and RTT estimation.

## How do you assign jobs to the three paths?

- Upon start, the range length is checked: if too short, start the same request on all three connections and see who finishes first.
- If the length is sufficiently large, check if we have bandwidth measurements.  If no such measurements available:
  - Start the measurements so that we can have them in next round.
  - Split the range into 3 equal ranges and start a request on each connection.
  - When any of the connections comes near `end-rtt*bw`, choke the rest 2 connections.  We now have 2 fragmented ranges.
  - Start splitting the 2 fragmented ranges, one after finishing another.
- If measurements are available:
  - Split according to bandwidth ratio.  Make sure that no ranges are smaller than 1 byte (or server would possibly reply 416 - Requested Range Not Satisfiable).
  - The rest is the same as above.

Note that we do not limit the number of running flows on a HTTP/2 connection, which may incur a small framing overhead (but no handshake overheads), but will workaround choking logic imperfections.  We are more likely to not leave a path idle in this way.

## What features (pipelining, eliminating tail byes, etc.) do you implement? And how do you implement them? 

- Basic pipelining is implemented: subflow end mark is judged via `end-rtt*bw` instead of just `end`.
- Tail bytes elimination is implemented: `ChokeAt` is used to terminate a stream instead of simply closing response body (i.e. sending `RST_FRAME`).
- `plot.gnu` is automatically generated according to run data.

Other design aspects are described in the _Implementation Overview_ section at the beginning.  

## Comments

Other HTTP multipath ramge splitting strategies also exist: for example, Internet Download Manager (IDM) starts multiple connections to fetch content faster.  Instead of doing bandwidth measurement, it simply starts new requests when a request finishes, maintaining constant a connection count.  

Apart from real multipath environments, creating multiple connections may also help fight with congestion in the Internet: increasing connection count increases the total QoS share, which in turn increases throughput.  Such method has been proved to be very effective in IDM.