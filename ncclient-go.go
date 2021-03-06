package ncclient

import (
	"bufio"
	"bytes"
	"code.google.com/p/go.crypto/ssh"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"time"
)

const NETCONF_DELIM string = "]]>]]>"
const NETCONF_HELLO string = `
<?xml version="1.0" encoding="UTF-8"?>
<nc:hello xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0">
	<nc:capabilities>
		<nc:capability>urn:ietf:params:netconf:capability:writable-running:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:rollback-on-error:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:validate:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:confirmed-commit:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:url:1.0?scheme=http,ftp,file,https,sftp</nc:capability>
		<nc:capability>urn:ietf:params:netconf:base:1.0</nc:capability>
		<nc:capability>urn:liberouter:params:netconf:capability:power-control:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:candidate:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:xpath:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:startup:1.0</nc:capability>
		<nc:capability>urn:ietf:params:netconf:capability:interleave:1.0</nc:capability>
	</nc:capabilities>
</nc:hello>
`

type clientPassword string

func (p clientPassword) Password(user string) (string, error) {
	return string(p), nil
}

type Ncclient struct {
	username string
	password string
	hostname string
	key      string
	port     int
	timeout  time.Duration

	sshClient     *ssh.Client
	session       *ssh.Session
	sessionStdin  io.WriteCloser
	sessionStdout io.Reader
}

func (n Ncclient) Hostname() string {
	return n.hostname
}

func (n Ncclient) Close() {
	n.session.Close()
	n.sshClient.Close()
}

func (n Ncclient) SendHello() (io.Reader, error) {
	reader, err := n.Write(NETCONF_HELLO)
	return reader, err
}

// TODO: use the xml module to add/remove rpc related tags
func (n Ncclient) WriteRPC(line string) (io.Reader, error) {
	line = fmt.Sprintf("<rpc>%s</rpc>", line)
	return n.Write(line)
}

func (n Ncclient) Write(line string) (result io.Reader, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(runtime.Error); ok {
				panic(r)
			}
			err = errors.New(r.(string))
		}
	}()

	if _, err := io.WriteString(n.sessionStdin, line+NETCONF_DELIM); err != nil {
		panic(err)
	}

	finished := make(chan *bytes.Buffer, 1)

	go func() {
		xmlBuffer := bytes.NewBufferString("")
		scanner := bufio.NewScanner(n.sessionStdout)
		for scanner.Scan() {
			line := scanner.Text()
			if line == NETCONF_DELIM {
				finished <- xmlBuffer
				break
			}
			xmlBuffer.WriteString(line + "\n")
		}
	}()

	select {
	case result := <-finished:
		return result, err
	case <-time.After(n.timeout):
		panic("Timed out waiting for NETCONF DELIMITER! Most likely a bad NETCONF speaker.")
	}
}

func MakeSshClient(username string, password string, hostname string, key string, port int) (*ssh.Client, *ssh.Session, io.WriteCloser, io.Reader) {

	var config *ssh.ClientConfig

	if key != "" {
		signer, _ := ssh.ParsePrivateKey([]byte(key))

		config = &ssh.ClientConfig{
			User: username,
			Auth: []ssh.AuthMethod{
				ssh.PublicKeys(signer),
				ssh.Password(password),
			},
		}
	} else {
		config = &ssh.ClientConfig{
			User: username,
			Auth: []ssh.AuthMethod{
				ssh.Password(password),
			},
		}
	}

	client, err := ssh.Dial("tcp", fmt.Sprintf("%s:%s", hostname, strconv.Itoa(port)), config)
	if err != nil {
		panic("Failed to dial:" + hostname + err.Error())
	}

	session, err := client.NewSession()
	if err != nil {
		panic("Failed to create session: " + err.Error())
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		panic(err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		panic(err)
	}
	return client, session, stdin, stdout
}

func (n *Ncclient) Connect() (err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(runtime.Error); ok {
				panic(r)
			}
			err = errors.New(r.(string))
		}
	}()
	sshClient, sshSession, sessionStdin, sessionStdout := MakeSshClient(n.username, n.password, n.hostname, n.key, n.port)

	if err := sshSession.RequestSubsystem("netconf"); err != nil {
		// TODO: the command `xml-mode netconf need-trailer` can be executed
		// as a  backup if the netconf subsystem is not available, try that if we fail
		sshClient.Close()
		sshSession.Close()
		panic("Failed to make subsystem request: " + err.Error())
	}
	n.sshClient = sshClient
	n.session = sshSession
	n.sessionStdin = sessionStdin
	n.sessionStdout = sessionStdout
	return err
}

func MakeClient(username string, password string, hostname string, key string, port int) Ncclient {
	nc := new(Ncclient)
	nc.username = username
	nc.password = password
	nc.hostname = hostname
	nc.key = key
	nc.port = port
	nc.timeout = time.Second * 30
	return *nc
}
