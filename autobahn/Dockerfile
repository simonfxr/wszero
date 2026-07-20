FROM golang:1.24-alpine AS go-builder

WORKDIR /gobuild
ENV GOTOOLCHAIN=auto

COPY go.mod go.sum interop.go.mod interop.go.sum ./
RUN go mod download -modfile interop.go.mod

COPY *.go ./
COPY examples/autobahn ./examples/autobahn

ENV CGO_ENABLED=0
RUN go test -modfile interop.go.mod -c -coverpkg "./..." ./examples/autobahn/.

FROM crossbario/autobahn-testsuite:latest

VOLUME /report

WORKDIR /
COPY --from=go-builder /gobuild/autobahn.test ./autobahn.test

COPY autobahn/config /config
COPY autobahn/entrypoint.sh ./
RUN chmod +x entrypoint.sh

ENTRYPOINT ["./entrypoint.sh"]
CMD []
