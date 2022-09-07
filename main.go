package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/facebookgo/pidfile"
	"github.com/nats-io/nats.go"
	flags "github.com/jessevdk/go-flags"
)

type HookList struct {
	Hooks []HookItem
}

type HookItem struct {
	Subject string
	Exec    string
	Workdir string
}

func runCmd(h HookItem, msg *nats.Msg) error {
	commands := strings.Fields(h.Exec)
	cmd := exec.Command(commands[0], commands[1:]...)
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

	if err := cmd.Run(); err != nil {
		return err
	}
    if err := msg.Respond(stdout.Bytes()); err != nil {
        return err
    }

	errCode := cmd.ProcessState.ExitCode()
	if errCode != 0 {
		return fmt.Errorf("Failed with error code: %d", errCode)
	}
	return nil
}

func setupConn(addr string) (*nats.Conn, error) {
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
	return nats.Connect(nats.DefaultURL, opts...)
}

func main() {
	var options struct {
		Addr    string `short:"a" long:"addr" description:"Address to listen on" default:":4222"`
		Hook    string `short:"f" long:"file" description:"Path to the toml file containing hooks definition" required:"true"`
		PidPath string `long:"pid" description:"Create PID file at the given path"`
	}

	if _, err := flags.ParseArgs(&options, os.Args); err != nil {
		if fe, ok := err.(*flags.Error); ok && fe.Type == flags.ErrHelp {
			os.Exit(0)
		}
		log.Fatal(err)
	}

	var hooks HookList
	_, err := toml.DecodeFile(options.Hook, &hooks)
	if err != nil {
		log.Fatal(err)
	}

	nc, err := setupConn(options.Addr)
	if err != nil {
		log.Fatal(err)
	}

	for i, _ := range hooks.Hooks {
		h := hooks.Hooks[i]

		log.Printf("Subscribe subject '%s'", h.Subject)
		nc.Subscribe(h.Subject, func(msg *nats.Msg) {
			log.Printf("Execute command '%s' on subject '%s'", h.Exec, h.Subject)
			if err := runCmd(h, msg); err != nil {
				log.Printf("Error: %s", err)
			}
		})
	}
	nc.Flush()

	if options.PidPath != "" {
		pidfile.SetPidfilePath(options.PidPath)
		if err := pidfile.Write(); err != nil {
			log.Fatal(err)
		}
		defer os.Remove(options.PidPath)
	}

	if err := nc.LastError(); err != nil {
		log.Fatal(err)
	}

	runtime.Goexit()
}
