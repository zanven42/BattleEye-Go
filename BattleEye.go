// Package BattleEye doco goes here
package BattleEye

import (
	"errors"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"time"
)

// Config documentation
type Config struct {
	addr     *net.UDPAddr
	Password string
	// time in seconds to wait for a response. defaults to 2.
	ConnTimeout uint32
	// time in seconds to wait for response. defaults to 1.
	ResponseTimeout uint32
	// wait time after first command response to check if multiple packets arrive.
	// defaults. 0.5s
	MultiResponseTimeout uint32
	// Time in seconds between sending a heartbeat when no commands are being sent. defaults 5
	HeartBeatTimer uint32
}

// GetConfig Returns the config to satisfy the interface for setting up a new battleeye connection
func (bec Config) GetConfig() Config {
	return bec
}

// BeConfig is the interface for passing in a configuration for the client.
// this allows other types to be implemented that also contain the type desired
type BeConfig interface {
	GetConfig() Config
}
type transmission struct {
	packet   []byte
	sequence byte
	sent     time.Time
	w        io.Writer
}

//--------------------------------------------------

// This Struct holds the State of the connection and all commands
type battleEye struct {

	// Passed in config

	password             string
	addr                 *net.UDPAddr
	connTimeout          uint32
	responseTimeout      uint32
	multiResponseTimeout uint32
	heartbeatTimer       uint32

	// Sequence byte to determine the packet we are up to in the chain.
	sequence struct {
		sync.Locker
		n byte
	}
	chatWriter  *io.Writer
	writebuffer []byte

	conn              *net.UDPConn
	lastCommandPacket struct {
		sync.Locker
		time.Time
	}
	running bool
	wg      sync.WaitGroup

	// use this to unlock before reading.
	// and match reads to waiting confirms to purge this list.
	// or possibly resend
	packetQueue []transmission
}

// New Creates and Returns a new Client
func New(config BeConfig) *battleEye {
	// setup all variables
	cfg := config.GetConfig()
	if cfg.ConnTimeout == 0 {
		cfg.ConnTimeout = 2
	}
	if cfg.ResponseTimeout == 0 {
		cfg.ResponseTimeout = 1
	}
	if cfg.HeartBeatTimer == 0 {
		cfg.HeartBeatTimer = 5
	}

	return &battleEye{
		password:        cfg.Password,
		addr:            cfg.addr,
		connTimeout:     cfg.ConnTimeout,
		responseTimeout: cfg.ResponseTimeout,
		heartbeatTimer:  cfg.HeartBeatTimer,
		writebuffer:     make([]byte, 4096),
	}

}

// Not Implemented
func (be *battleEye) SendCommand(command []byte, w io.Writer) error {
	be.sequence.Lock()
	sequence := be.sequence.n
	// increment the sending packet.
	if be.sequence.n == 255 {
		be.sequence.n = 0
	} else {
		be.sequence.n++
	}
	be.sequence.Unlock()

	packet := buildCommandPacket(command, sequence)
	be.conn.SetWriteDeadline(time.Now().Add(time.Second * time.Duration(be.responseTimeout)))
	be.conn.Write(packet)

	be.lastCommandPacket.Lock()
	be.lastCommandPacket.Time = time.Now()
	be.lastCommandPacket.Unlock()

	/*
		be.conn.SetReadDeadline(time.Now().Add(time.Second * time.Duration(be.responseTimeout)))

		// have to somehow look for multi Packet with this shit,
		// and handle when i am reading irelevent information.
		n, err := be.conn.Read(be.writebuffer)
		if err != nil {
			return err
		}
		w.Write(be.writebuffer[:n])
	*/

	return nil
}

func (be *battleEye) Connect() (bool, error) {
	be.wg = sync.WaitGroup{}
	var err error
	// dial the Address
	be.conn, err = net.DialUDP("udp", nil, be.addr)
	if err != nil {
		return false, err
	}
	// make a buffer to read the packet packed with extra space
	packet := make([]byte, 9)

	// set timeout deadline so we dont block forever
	be.conn.SetReadDeadline(time.Now().Add(time.Second * 2))
	// Send a Connection Packet
	be.conn.Write(buildConnectionPacket(be.password))
	// Read connection and hope it doesn't time out and the server responds
	n, err := be.conn.Read(packet)
	// check if this is a timeout error.
	if err, ok := err.(net.Error); ok && err.Timeout() {
		return false, errors.New("Connection Timed Out")
	}
	if err != nil {
		return false, err
	}

	result, err := checkLogin(packet[:n])
	if err != nil {
		return false, err
	}

	if result == packetResponse.LoginFail {
		return false, nil
	}

	// nothing has failed we are good to go :).
	// Spin up a go routine to read back on a connection
	be.wg.Add(1)
	//go
	return true, nil
}

func (be *battleEye) updateLoop() {
	defer be.wg.Done()
	for {
		if be.conn == nil {
			return
		}
		t := time.Now()

		be.lastCommandPacket.Lock()
		if t.After(be.lastCommandPacket.Add(time.Second * time.Duration(be.heartbeatTimer))) {
			err := be.SendCommand([]byte{}, ioutil.Discard)
			if err != nil {
				return
			}
			be.lastCommandPacket.Time = t
		}
		be.lastCommandPacket.Unlock()

		// do check for new incoming data
		be.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
		n, err := be.conn.Read(be.writebuffer)
		if err != nil {
			continue
		}
		data := be.writebuffer[:n]
		be.processPacket(data)
	}
}
func (be *battleEye) processPacket(data []byte) {
	// validate packet is good.

	// remove header

	// if say command write and leave

	// else for command check if we expect more packets and how many.

	// process the packet if we have no more

	// loop till we have all the messages and i guess send confirms back.
}
func (be *battleEye) Disconnect() error {
	// maybe also close the main loop and wait for that?
	be.conn.Close()
	be.wg.Wait()
	return nil
}

func checkLogin(packet []byte) (byte, error) {
	var err error
	if len(packet) != 9 {
		return 0, errors.New("Packet Size Invalid for Response")
	}
	// check if we have a valid packet
	if match, err := packetMatchesChecksum(packet); match == false || err != nil {
		return 0, err
	}
	// now check if we got a success or a fail
	// 2 byte prefix. 4 byte checksum. 1 byte terminate header. 1 byte login type. 1 byte result
	return packet[8], err
}
