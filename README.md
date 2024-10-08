# Lightweight WebSocket Library for Go

This is a high-performance, lightweight WebSocket library for Go.

## Features

- High performance, usually several times faster than other tested libraries (see below)
- Zero allocations
- Zero copy [writev](https://pkg.go.dev/net#Buffers) path when writing fragments 
- Support for both client and server implementations
- Customizable buffer pool for efficient memory management
- High level API heavily inspired by the popular [gorilla/websocket](https://github.com/gorilla/websocket)
- No third party dependencies
- Tested with [autobahn-testsuite](https://github.com/crossbario/autobahn-testsuite)

## Missing features

- UTF-8 validation of text messages, this can be handled at the application layer
- WebSocket compression

## Installation

To install the library, use `go get`:

```bash
go get github.com/simonfxr/wszero
```

## Quick Start

### Server Example

Here's a simple example of how to create a WebSocket server:

```go
package main

import (
	"log"
	"net/http"

	"github.com/simonfxr/wszero"
)

func main() {
	upgrader := &wszero.Upgrader{}

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("Upgrade error:", err)
			return
		}
		defer conn.Close()

		for {
			messageType, p, err := conn.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				return
			}
			if err := conn.WriteMessage(messageType, p); err != nil {
				log.Println("Write error:", err)
				return
			}
			// (optional) return buffer to the pool
			conn.BufferPool().PutBuffer(p)
		}
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### Client Example

Here's how to create a WebSocket client:

```go
package main

import (
	"context"
	"github.com/simonfxr/wszero"
	"log"
)

func main() {
	dialer := wszero.DefaultDialer

	conn, _, err := dialer.DialContext(context.Background(), "ws://localhost:8080/ws", nil)
	if err != nil {
		log.Fatal("Dial error:", err)
	}
	defer conn.Close()

	err = conn.WriteMessageString(wszero.TextMessage, "Hello, WebSocket!")
	if err != nil {
		log.Fatal("Write error:", err)
	}

	_, message, err := conn.ReadMessage()
	if err != nil {
		log.Fatal("Read error:", err)
	}

	log.Printf("Received: %s", message)
}
```

## Documentation

For detailed documentation, please refer to the [GoDoc](https://pkg.go.dev/github.com/simonfxr/wszero) page.

## Autobahn Testsuite

The implementation passes all tests in the [Autobahn Testsuite](https://github.com/crossbario/autobahn-testsuite) except those that are related to the explicitly missing features listed above (UTF-8 validation of text messages and message compression).

## Benchmarks comparisons with other implementations

Benchmarks results on an AMD Ryzen 9 7950X using go 1.22.4.

### WriteMessage benchmarks

```
BenchmarkWriteMessage/wszero[msgsz=2]-12              797.5 ns/op         2.51 MB/s           0 B/op     0 allocs/op
BenchmarkWriteMessage/websocket[msgsz=2]-12            1403 ns/op         1.43 MB/s          76 B/op     3 allocs/op
BenchmarkWriteMessage/gobwas[msgsz=2]-12               2017 ns/op         0.99 MB/s          40 B/op     3 allocs/op
BenchmarkWriteMessage/nhooyr[msgsz=2]-12               1454 ns/op         1.38 MB/s           0 B/op     0 allocs/op

BenchmarkWriteMessage/wszero[msgsz=128]-12             1069 ns/op       119.72 MB/s           0 B/op     0 allocs/op
BenchmarkWriteMessage/websocket[msgsz=128]-12          1421 ns/op        90.08 MB/s          76 B/op     3 allocs/op
BenchmarkWriteMessage/gobwas[msgsz=128]-12             1931 ns/op        66.30 MB/s          40 B/op     2 allocs/op
BenchmarkWriteMessage/nhooyr[msgsz=128]-12             1577 ns/op        81.16 MB/s           0 B/op     0 allocs/op

BenchmarkWriteMessage/wszero[msgsz=32768]-12           2281 ns/op      14365.28 MB/s          0 B/op     0 allocs/op
BenchmarkWriteMessage/websocket[msgsz=32768]-12       11990 ns/op       2732.87 MB/s        104 B/op    10 allocs/op
BenchmarkWriteMessage/gobwas[msgsz=32768]-12           3987 ns/op       8218.34 MB/s         40 B/op     2 allocs/op
BenchmarkWriteMessage/nhooyr[msgsz=32768]-12           8418 ns/op       3892.80 MB/s          0 B/op     0 allocs/op

BenchmarkWriteMessage/wszero[msgsz=262144]-12         14829 ns/op      17678.03 MB/s          0 B/op     0 allocs/op
BenchmarkWriteMessage/websocket[msgsz=262144]-12     114598 ns/op       2287.51 MB/s        328 B/op    66 allocs/op
BenchmarkWriteMessage/gobwas[msgsz=262144]-12         57441 ns/op       4563.70 MB/s     262186 B/op     3 allocs/op
BenchmarkWriteMessage/nhooyr[msgsz=262144]-12         75932 ns/op       3452.36 MB/           0 B/op     0 allocs/op
```

### ReadMessage benchmarks

```
BenchmarkReadMessage/client/wszero[msgsz=2]-12                        19.99 ns/op     200.10 MB/s         0 B/op      0 allocs/op
BenchmarkReadMessage/client/websocket[msgsz=2]-12                     123.2 ns/op      32.47 MB/s       520 B/op      2 allocs/op
BenchmarkReadMessage/client/gobwas[msgsz=2]-12                        657.6 ns/op       6.08 MB/s       720 B/op      3 allocs/op
BenchmarkReadMessage/client/nhooyr[msgsz=2]-12                         1124 ns/op       3.56 MB/s       512 B/op      1 allocs/op

BenchmarkReadMessage/client/wszero[msgsz=128]-12                      42.45 ns/op    3109.90 MB/s         0 B/op      0 allocs/op
BenchmarkReadMessage/client/websocket[msgsz=128]-12                   166.7 ns/op     791.96 MB/s       520 B/op      2 allocs/op
BenchmarkReadMessage/client/gobwas[msgsz=128]-12                      861.3 ns/op     153.25 MB/s       720 B/op      3 allocs/op
BenchmarkReadMessage/client/nhooyr[msgsz=128]-12                       1188 ns/op     111.09 MB/s       512 B/op      1 allocs/op

BenchmarkReadMessage/client/wszero[msgsz=32768]-12                   3008 ns/op     10894.07 MB/s         0 B/op      0 allocs/op
BenchmarkReadMessage/client/websocket[msgsz=32768]-12               28812 ns/op      1137.43 MB/s    153866 B/op     15 allocs/op
BenchmarkReadMessage/client/gobwas[msgsz=32768]-12                  26988 ns/op      1214.30 MB/s    154066 B/op     16 allocs/op
BenchmarkReadMessage/client/nhooyr[msgsz=32768]-12                  47741 ns/op       686.46 MB/s    153860 B/op     14 allocs/op


BenchmarkReadMessage/server-randmask/wszero[msgsz=2]-12               22.09 ns/op    362.15 MB/s         0 B/op      0 allocs/op
BenchmarkReadMessage/server-randmask/websocket[msgsz=2]-12           137.9 ns/op      58.03 MB/s       520 B/op      2 allocs/op
BenchmarkReadMessage/server-randmask/gobwas[msgsz=2]-12              860.7 ns/op       9.29 MB/s       752 B/op      4 allocs/op
BenchmarkReadMessage/server-randmask/nhooyr[msgsz=2]-12               1071 ns/op       7.47 MB/s       512 B/op      1 allocs/op

BenchmarkReadMessage/server-randmask/wszero[msgsz=128]-12             44.20 ns/op   3077.26 MB/s         0 B/op      0 allocs/op
BenchmarkReadMessage/server-randmask/websocket[msgsz=128]-12         179.4 ns/op     757.95 MB/s       520 B/op      2 allocs/op
BenchmarkReadMessage/server-randmask/gobwas[msgsz=128]-12            895.6 ns/op     151.86 MB/s       752 B/op      4 allocs/op
BenchmarkReadMessage/server-randmask/nhooyr[msgsz=128]-12             1150 ns/op     118.28 MB/s       512 B/op      1 allocs/op

BenchmarkReadMessage/server-randmask/wszero[msgsz=32768]-12         2931 ns/op     11182.41 MB/s        0 B/op      0 allocs/op
BenchmarkReadMessage/server-randmask/websocket[msgsz=32768]-12     30984 ns/op      1057.83 MB/s   153866 B/op     15 allocs/op
BenchmarkReadMessage/server-randmask/gobwas[msgsz=32768]-12        29046 ns/op      1128.42 MB/s   154098 B/op     17 allocs/op
BenchmarkReadMessage/server-randmask/nhooyr[msgsz=32768]-12        49740 ns/op       658.95 MB/s   153860 B/op     14 allocs/op
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the [MIT License](LICENSE).

# Similar projects

Here is a selection of some existing similar projects

- [gorilla/websocket](https://github.com/gorilla/websocket)
- [gobwas/ws](https://github.com/gobwas/ws)
- [nhooyr/websocket](https://github.com/nhooyr/websocket)
