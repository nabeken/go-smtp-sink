# go-smtp-sink

`go-smtp-sink` is a simple smtp-sink written in Go as a testbed SMTP implementation.

## Usage

```sh
go-smtp-sink is a SMTP Sink server written in Go.

Usage:
  go-smts-sink [flags] IP_ADDR:PORT

Flags:
      --cert string               specify a path to load public SSL certificate
      --disable-session-tickets   specify to disable TLS Session Tickets
  -h, --help                      help for go-smts-sink
      --key string                specify a path to load private SSL certificate
      --server-name string        specify a server name (default "mx.example.com")
      --use-key-log               specify to use TLS Key Log
      --use-tls12                 specify to use TLS 1.2 only
```
