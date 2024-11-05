package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	if err := realmain(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

type sessionState int

const (
	beforeEHLO sessionState = iota
	beforeMAIL
	beforeRCPT
	beforeDATA
	inDATA
	afterDATA
)

type session struct {
	client string
	state  sessionState
	tx     *transaction

	inTLS bool
}

func (s *session) init() {
	s.client = ""
	s.tx = nil
}

type transaction struct {
	mailFrom string
	rcptTo   []string
	data     []byte
}

func realmain() error {
	var serverName string
	var certPath string
	var keyPath string
	var useTLS12 bool

	rootCmd := &cobra.Command{
		Use:   "go-smts-sink",
		Short: "go-smtp-sink is a SMTP Sink server written in Go.",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) < 1 {
				cmd.PrintErr("please specify an address to listen to\n")
				return
			}

			addr := args[0]

			slog.Info(fmt.Sprintf("Listening to %s...", addr))

			srv, err := NewServer(serverName, certPath, keyPath, useTLS12)
			if err != nil {
				slog.Error("Failed to create a server", "error", err.Error())
				return
			}

			l, err := net.Listen("tcp", addr)
			if err != nil {
				slog.Error("Failed to listen", "error", err.Error())
				return
			}

			defer l.Close()

			for {
				func() {
					conn, err := l.Accept()
					if err != nil {
						slog.Error("Failed to accept", "error", err.Error())
						return
					}

					srv.serveConn(conn)
				}()
			}
		},
	}

	rootCmd.Flags().StringVar(
		&serverName,
		"server-name",
		"mx.example.com",
		"specify a server name",
	)

	rootCmd.Flags().StringVar(
		&certPath,
		"cert",
		"",
		"specify a path to load public SSL certificate",
	)
	rootCmd.Flags().StringVar(
		&keyPath,
		"key",
		"",
		"specify a path to load private SSL certificate",
	)
	rootCmd.Flags().BoolVar(
		&useTLS12,
		"use-tls12",
		false,
		"specify to use TLS 1.2 only",
	)
	return rootCmd.Execute()
}

type server struct {
	hostname  string
	tlsConfig *tls.Config
}

func NewServer(hostname, certPath, keyPath string, useTLS12 bool) (*server, error) {
	srv := server{hostname: hostname}

	if certPath == "" || keyPath == "" {
		slog.Info("Skipped loading cert and key")
		return &srv, nil
	}

	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		slog.Error("Failed to load the certificates", "error", err.Error())
		return nil, err
	}

	tlsConfig := tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}

	if useTLS12 {
		tlsConfig.MinVersion = tls.VersionTLS12
		tlsConfig.MaxVersion = tls.VersionTLS12
	}

	return &server{
		hostname:  hostname,
		tlsConfig: &tlsConfig,
	}, nil
}

func (s *server) serveConn(conn net.Conn) {
	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)

	writeReplyAndFlush(bw, 220, fmt.Sprintf("%s ESMTP", s.hostname))

	sess := &session{}

	filename := fmt.Sprintf("%d-%s.dat", time.Now().UTC().Unix(), conn.RemoteAddr().(*net.TCPAddr).IP.String())
	f, err := os.Create(filename)
	if err != nil {
		slog.Error("Failed to create a log file", "error", err.Error())
		return
	}
	defer f.Close()

	fmt.Fprint(f, "=== SESSION BEGIN ===\n")

	slog.Info("Writing Connection log", "file", f.Name())

	conn = &logConn{
		inner: conn,
		w:     f,
	}

	var quit bool
	for !quit {
		verb, args, err := readCommand(br)

		if err != nil {
			slog.Error("Failed to read the command", "error", err.Error())
			writeReplyAndFlush(bw, 550, "Requested action not taken")
			quit = true
			break
		}
		// TODO:
		//  DATA

		// DONE:
		//  MAIL
		//  RCPT
		// 	EHLO
		// 	HELO
		// 	RSET
		// 	NOOP
		// 	QUIT
		// 	VRFY

		switch verb {
		case "EHLO", "HELO":
			// reset to an initial state
			sess.init()

			if args == "" {
				args = "unknown"
			}

			sess.client = args
			sess.state = beforeMAIL

			reply := []string{
				fmt.Sprintf("%s greets %s", s.hostname, sess.client),
			}

			if s.tlsConfig != nil {
				if !sess.inTLS {
					// advertise STARTTLS extension
					reply = append(reply, "STARTTLS")
				}
			}

			writeReplyAndFlush(
				bw,
				250,
				reply...,
			)

		case "MAIL":
			if sess.state != beforeMAIL {
				respBadSequenceOfCommands(bw)
				continue
			}

			sess.tx = &transaction{}

			// TODO: handle Mail-parameters
			mailFrom := readMAILCommand(args)
			if mailFrom == "" {
				respInvalidSyntax(bw)
				continue
			}

			slog.Info("Received MAIL FROM", "mail_from", mailFrom)

			sess.tx.mailFrom = mailFrom
			sess.state = beforeRCPT

			respOK(bw)

		case "RCPT":
			if sess.state != beforeRCPT {
				respBadSequenceOfCommands(bw)
				continue
			}

			// TODO: handle Rcpt-parameters
			rcptTo := readRCPTCommand(args)
			if rcptTo == "" {
				respInvalidSyntax(bw)
				continue
			}

			// TODO: check the total number of recipients

			slog.Info("Received RCPT TO", "rcpt_to", rcptTo)

			sess.tx.rcptTo = append(sess.tx.rcptTo, rcptTo)
			sess.state = beforeDATA

			respOK(bw)

		case "DATA":
			if sess.state != beforeDATA {
				respBadSequenceOfCommands(bw)
				continue
			}

			writeReplyAndFlush(bw, 354, "Start mail input; end with <CRLF>.<CRLF>")
			sess.state = inDATA

			// limit to 30MB
			lr := io.LimitReader(br, 1024*1024*30)
			tr := textproto.NewReader(bufio.NewReader(lr))
			dr := tr.DotReader()

			data, err := io.ReadAll(dr)
			if err != nil {
				slog.Error("Failed to read DATA", "error", err.Error())
			}

			sess.tx.data = data

			fmt.Printf("=== BODY BEGIN ==\n%s=== BODY END ===\n", string(data))

			// TODO: processing
			respOK(bw)

			sess.state = afterDATA

		case "QUIT":
			quit = true
		case "NOOP":
			respOK(bw)

		case "RSET":
			sess.state = beforeMAIL
			sess.tx = &transaction{}
			respOK(bw)

		case "VRFY":
			writeReplyAndFlush(bw, 502, "Command not implemented")

		case "STARTTLS":
			writeReplyAndFlush(bw, 220, "Ready to start TLS")

			tlsConn := tls.Server(conn, s.tlsConfig)
			err := tlsConn.Handshake()

			connState := tlsConn.ConnectionState()
			connStateLogValue := slog.GroupValue(
				slog.String("version", tls.VersionName(connState.Version)),
				slog.Bool("handshake_complete", connState.HandshakeComplete),
				slog.Bool("did_resume", connState.DidResume),
				slog.String("cipher_suite", tls.CipherSuiteName(connState.CipherSuite)),
				slog.String("negotiated_protocol", connState.NegotiatedProtocol),
				slog.String("server_name", connState.ServerName),
			)

			slog.Info("TLS handshake completed",
				"error", err,
				"connection_state", connStateLogValue,
			)

			if err != nil {
				writeReplyAndFlush(bw, 454, "TLS not available due to temporary reason")
				continue
			}

			sess.inTLS = true

			// reinstall bw and br
			conn = tlsConn
			br = bufio.NewReader(conn)
			bw = bufio.NewWriter(conn)

		default:
			slog.Info("Unrecognized command received", "command", verb, "args", args)
			writeReplyAndFlush(bw, 500, "Syntax error")
		}
	}

	defer conn.Close()

	writeReplyAndFlush(bw, 221, "Service closing transmission channel")
	fmt.Fprint(f, "=== SESSION END ===\n")
}

func respInvalidSyntax(bw *bufio.Writer) {
	writeReplyAndFlush(bw, 501, "Syntax error in parameters or arguments")
}

func respBadSequenceOfCommands(bw *bufio.Writer) {
	writeReplyAndFlush(bw, 503, "Bad sequence of commands")
}

func respOK(bw *bufio.Writer) {
	writeReplyAndFlush(bw, 250, "OK")
}

func writeReplyAndFlush(bw *bufio.Writer, code int, reply ...string) {
	for i := 0; i < len(reply); i++ {
		if i+1 == len(reply) {
			fmt.Fprintf(bw, "%d %s\r\n", code, reply[i])
		} else {
			fmt.Fprintf(bw, "%d-%s\r\n", code, reply[i])
		}
	}

	if err := bw.Flush(); err != nil {
		slog.Info("Failed to flush the write", "error", err.Error())
	}
}

func readMAILCommand(args string) string {
	if len(args) < len("FROM:") {
		return ""
	}

	if strings.EqualFold("FROM:", args[:len("FROM:")]) {
		return strings.TrimSpace(args[len("FROM:"):])
	}

	return ""
}

func readRCPTCommand(args string) string {
	if len(args) < len("TO:") {
		return ""
	}

	if strings.EqualFold("TO:", args[:len("TO:")]) {
		return strings.TrimSpace(args[len("TO:"):])
	}

	return ""
}

func readCommand(br *bufio.Reader) (string, string, error) {
	l_, err := br.ReadString('\n')
	if err != nil {
		return "", "", err
	}

	l := strings.Trim(strings.Trim(l_, "\n"), "\r")

	cmd := strings.SplitN(l, " ", 2)

	if len(cmd) > 1 {
		return strings.ToUpper(cmd[0]), cmd[1], nil
	}

	return strings.ToUpper(cmd[0]), "", nil
}
