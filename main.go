package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/facebookgo/pidfile"
	flags "github.com/jessevdk/go-flags"
	"github.com/nats-io/nats.go"
	"github.com/rs/xid"
)

type HookList struct {
	Hooks []HookItem
}

type HookItem struct {
	Subject string
	Workdir string
	Command string
	Inline  string
}

func ellipsis(text string, length int) string {
	r := []rune(text)
	if len(r) > length {
		return string(r[0:length]) + "..."
	}
	return text
}

func runCmd(h HookItem, msg *nats.Msg, logger *log.Logger) error {
	var commands []string
	if h.Inline != "" {
		logger.Printf("Execute command \"sh\"")
		commands = append(commands, "sh", "-c", h.Inline)
	} else if h.Command != "" {
		logger.Printf("Execute command \"%s\"", ellipsis(h.Command, 80))
		commands = append(commands, strings.Fields(h.Command)...)
	} else {
		return nil
	}

	cmd := exec.Command(commands[0], commands[1:]...)
	cmd.Env = os.Environ()
	if h.Workdir != "" {
		cmd.Dir = h.Workdir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	stdin.Write(msg.Data)
	stdin.Close()

	err = cmd.Run()
	result := stdout.Bytes()
	if msg.Reply != "" {
		if err := msg.Respond(result); err != nil {
			return err
		}
	}
	summary := ellipsis(string(result), 80)
	logger.Printf("Result: %s", strconv.Quote(summary))

	if err != nil {
		return fmt.Errorf("Failed to run command: %s", err)
	}
	return nil
}

func setupConn(addr, userCreds, nkeyFile, tlsCert, tlsKey, tlsCACert string) (*nats.Conn, error) {
	totalWait := 10 * time.Minute
	reconnectDelay := time.Second

	opts := []nats.Option{nats.Name("NATS Hook")}
	opts = append(opts, nats.ReconnectWait(reconnectDelay))
	opts = append(opts, nats.MaxReconnects(int(totalWait/reconnectDelay)))
	opts = append(opts, nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
		log.Printf("Disconnected due to:%s, will attempt reconnects for %.0fm", err, totalWait.Minutes())
	}))
	opts = append(opts, nats.ReconnectHandler(func(nc *nats.Conn) {
		log.Printf("Reconnected [%s]", nc.ConnectedUrl())
	}))
	opts = append(opts, nats.ClosedHandler(func(nc *nats.Conn) {
		log.Fatalf("Exiting: %v", nc.LastError())
	}))

	if userCreds != "" {
		opts = append(opts, nats.UserCredentials(userCreds))
	} else if nkeyFile != "" {
		opt, err := nats.NkeyOptionFromSeed(nkeyFile)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}

	if tlsCert != "" && tlsKey != "" {
		opts = append(opts, nats.ClientCert(tlsCert, tlsKey))
	}

	if tlsCACert != "" {
		opts = append(opts, nats.RootCAs(tlsCACert))
	}

	return nats.Connect(addr, opts...)
}

func main() {
	var opts struct {
		Addr      string `short:"a" long:"addr" description:"Address to listen on" default:":4222"`
		Hook      string `short:"f" long:"file" description:"Path to the toml file containing hooks definition" required:"true"`
		PidPath   string `long:"pid" description:"Create PID file at the given path"`
		UserCreds string `long:"creds" description:"User Credentials File"`
		NKeyFile  string `long:"nkey" description:"NKey Seed File"`
		TlsCert   string `long:"tlscert" description:"TLS client certificate file"`
		TlsKey    string `long:"tlskey" description:"Private key file for client certificate"`
		TlsCACert string `long:"tlscacert" description:"CA certificate to verify peer against"`
	}

	if _, err := flags.ParseArgs(&opts, os.Args); err != nil {
		if fe, ok := err.(*flags.Error); ok && fe.Type == flags.ErrHelp {
			os.Exit(0)
		}
		log.Fatal(err)
	}

	var hooks HookList
	_, err := toml.DecodeFile(opts.Hook, &hooks)
	if err != nil {
		log.Fatal(err)
	}

	nc, err := setupConn(opts.Addr, opts.UserCreds, opts.NKeyFile, opts.TlsCert, opts.TlsKey, opts.TlsCACert)
	if err != nil {
		log.Fatal(err)
	}

	for i := range hooks.Hooks {
		h := hooks.Hooks[i]

		log.Printf("[%s]: Subscribed", h.Subject)
		nc.Subscribe(h.Subject, func(msg *nats.Msg) {
			go func(msg *nats.Msg) {
				prefix := fmt.Sprintf("[%s]: [%s] ", h.Subject, xid.New())
				logger := log.New(os.Stdout, prefix, log.LstdFlags|log.Lmsgprefix)
				if err := runCmd(h, msg, logger); err != nil {
					logger.Printf("Error: %s", err)
				}
			}(msg)
		})
	}
	nc.Flush()

	if opts.PidPath != "" {
		pidfile.SetPidfilePath(opts.PidPath)
		if err := pidfile.Write(); err != nil {
			log.Fatal(err)
		}
		defer os.Remove(opts.PidPath)
	}

	if err := nc.LastError(); err != nil {
		log.Fatal(err)
	}

	runtime.Goexit()
}
