package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xinlaoda/opentorque/internal/mom/config"
	"github.com/xinlaoda/opentorque/internal/mom/dis"
	"github.com/xinlaoda/opentorque/internal/mom/resource"
)

// IS protocol constants (Inter-Server protocol for status updates)
const (
	ISProtocol    = 4
	ISProtocolVer = 3
	ISClusterAddrs = 2
	ISUpdate       = 3
	ISStatus       = 4
	ISEOLMessage   = "END_OF_LINE"
)

// Connection manages communication with a pbs_server.
type Connection struct {
	mu       sync.Mutex
	cfg      *config.Config
	monitor  resource.Monitor
	host     string
	port     int
	lastUpdate time.Time
}

// NewConnection creates a new server connection.
func NewConnection(cfg *config.Config, monitor resource.Monitor, host string) *Connection {
	return &Connection{
		cfg:     cfg,
		monitor: monitor,
		host:    host,
		port:    cfg.ServerPort,
	}
}

// Connect tests that the server is reachable.
func (c *Connection) Connect() error {
	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to server %s: %w", addr, err)
	}
	conn.Close()
	log.Printf("[SERVER] Server %s is reachable", addr)
	return nil
}

// Close is a no-op since each status update uses its own connection.
func (c *Connection) Close() {}

// IsConnected returns true (we connect per-update).
func (c *Connection) IsConnected() bool {
	return true
}

// SendStatusUpdate sends a node status update to the server.
// This opens a NEW TCP connection each time, matching the C mom behavior.
// Protocol: IS_PROTOCOL(4) IS_PROTOCOL_VER(3) IS_STATUS(4)
//           mom_port(uint) rm_port(uint)
//           status strings... "END_OF_LINE"
func (c *Connection) SendStatusUpdate() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connect to server %s: %w", addr, err)
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	w := dis.NewWriter(conn)

	// IS protocol header (uses SIGNED int: diswsi)
	if err := w.WriteInt(ISProtocol); err != nil {
		return fmt.Errorf("write IS protocol: %w", err)
	}
	if err := w.WriteInt(ISProtocolVer); err != nil {
		return fmt.Errorf("write IS version: %w", err)
	}
	if err := w.WriteInt(ISStatus); err != nil {
		return fmt.Errorf("write IS command: %w", err)
	}

	// Mom ports (unsigned int: diswui/diswus)
	if err := w.WriteUint(uint64(c.cfg.MomPort)); err != nil {
		return fmt.Errorf("write mom port: %w", err)
	}
	if err := w.WriteUint(uint64(c.cfg.RMPort)); err != nil {
		return fmt.Errorf("write rm port: %w", err)
	}

	// Get node status
	status, err := c.monitor.GetNodeStatus()
	if err != nil {
		return fmt.Errorf("get node status: %w", err)
	}

	hostname, _ := os.Hostname()
	shortHost := strings.Split(hostname, ".")[0]

	// Write "node=<hostname>" first
	if err := w.WriteString(fmt.Sprintf("node=%s", shortHost)); err != nil {
		return err
	}

	// Request cluster addresses on first update
	if c.lastUpdate.IsZero() {
		if err := w.WriteString("first_update=true"); err != nil {
			return err
		}
	}

	// Write status strings
	statusItems := buildStatusString(status, shortHost)
	for _, item := range statusItems {
		if err := w.WriteString(item); err != nil {
			return err
		}
	}

	// End of line marker
	if err := w.WriteString(ISEOLMessage); err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return err
	}

	// Read reply from server
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	r := dis.NewReader(conn)
	// Reply format: diswsi(protocol) diswsi(version) diswsi(command) diswsi(status)
	_, _ = r.ReadInt() // protocol
	_, _ = r.ReadInt() // version
	_, _ = r.ReadInt() // command
	replyStatus, _ := r.ReadInt() // status

	c.lastUpdate = time.Now()
	log.Printf("[SERVER] Status update sent to %s (%d items, reply=%d)", c.host, len(statusItems), replyStatus)
	return nil
}

// SendJobObit sends a job obituary to the server using the Batch protocol.
// Tries token-based auth first (cross-platform), falls back to privileged port.
// Format: ReqHdr + JobObit body (jobid, status, svrattrl) + ReqExtend
func (c *Connection) SendJobObit(jobID string, exitStatus int, resources map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.host, c.port)

	// Try token-based auth first (works cross-platform, no root needed)
	conn, authMethod, err := c.connectWithAuth(addr)
	if err != nil {
		return fmt.Errorf("connect to server %s for obit: %w", addr, err)
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))

	w := dis.NewWriter(conn)

	// Request header
	if err := dis.WriteRequestHeader(w, dis.BatchJobObit, "root"); err != nil {
		return err
	}

	// Job ID (diswst)
	if err := w.WriteString(jobID); err != nil {
		return err
	}

	// Exit status (diswsi - signed int)
	if err := w.WriteInt(int64(exitStatus)); err != nil {
		return err
	}

	// Attribute list (encode_DIS_svrattrl format)
	// Count of attributes
	if err := w.WriteUint(uint64(len(resources))); err != nil {
		return err
	}

	// Each attribute: size(uint) name(str) hasResc(uint) [resc(str)] value(str) op(uint)
	for name, value := range resources {
		nameLen := len("resources_used") + len(name) + len(value) + 3
		if err := w.WriteUint(uint64(nameLen)); err != nil {
			return err
		}
		if err := w.WriteString("resources_used"); err != nil {
			return err
		}
		if err := w.WriteUint(1); err != nil { // hasResc = 1
			return err
		}
		if err := w.WriteString(name); err != nil { // resource name
			return err
		}
		if err := w.WriteString(value); err != nil { // value
			return err
		}
		if err := w.WriteUint(1); err != nil { // op = SET
			return err
		}
	}

	// Extension: encode_DIS_ReqExtend with NULL
	if err := w.WriteUint(0); err != nil {
		return err
	}

	if err := w.Flush(); err != nil {
		return err
	}

	// Read reply
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := dis.NewReader(conn)
	// Read proto, ver, code, auxcode, choice
	_, err = reader.ReadUint() // proto
	if err != nil {
		return fmt.Errorf("read obit reply proto: %w", err)
	}
	_, err = reader.ReadUint() // ver
	if err != nil {
		return fmt.Errorf("read obit reply ver: %w", err)
	}
	code, err := reader.ReadInt() // code
	if err != nil {
		return fmt.Errorf("read obit reply code: %w", err)
	}

	if code != 0 {
		return fmt.Errorf("obit rejected with code %d", code)
	}

	log.Printf("[SERVER] Sent job obit for %s (exit=%d, auth=%s) to %s", jobID, exitStatus, authMethod, c.host)
	return nil
}

// buildStatusString creates the status attributes list.
func buildStatusString(status *resource.NodeStatus, hostname string) []string {
	items := make([]string, 0, 20)

	items = append(items, fmt.Sprintf("opsys=%s", status.OSName))
	items = append(items, fmt.Sprintf("uname=%s %s %s",
		status.OSName, hostname, status.OSRelease))
	items = append(items, "sessions=")
	items = append(items, "nsessions=0")
	items = append(items, "nusers=0")
	items = append(items, fmt.Sprintf("ncpus=%d", status.Ncpus))
	items = append(items, fmt.Sprintf("loadave=%.2f", status.LoadAvg))
	items = append(items, fmt.Sprintf("totmem=%dkb", status.TotalMem))
	items = append(items, fmt.Sprintf("availmem=%dkb", status.AvailMem))
	items = append(items, fmt.Sprintf("physmem=%dkb", status.PhysMem))

	if status.TotalSwap > 0 {
		items = append(items, fmt.Sprintf("totswap=%dkb", status.TotalSwap))
		items = append(items, fmt.Sprintf("availswap=%dkb", status.AvailSwap))
	}

	items = append(items, fmt.Sprintf("idletime=%d", status.IdleTime))
	items = append(items, fmt.Sprintf("arch=%s", status.Arch))
	items = append(items, "state=free")
	items = append(items, "gres=")
	items = append(items, fmt.Sprintf("netload=0"))
	items = append(items, "varattr= ")
	items = append(items, fmt.Sprintf("cpuclock=Fixed"))
	items = append(items, fmt.Sprintf("version=7.0.0-go"))
	items = append(items, fmt.Sprintf("rectime=%d", time.Now().Unix()))
	items = append(items, "jobs=")

	return items
}

// connectWithAuth tries token auth first, then falls back to privileged port.
// Returns the connection and a string describing the auth method used.
func (c *Connection) connectWithAuth(addr string) (net.Conn, string, error) {
	// Try token auth: connect from any port and authenticate with HMAC token
	pbsHome := os.Getenv("PBS_HOME")
	if pbsHome == "" {
		pbsHome = "/var/spool/torque"
	}
	keyPath := filepath.Join(pbsHome, authKeyFileName)
	if _, err := os.Stat(keyPath); err == nil {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err == nil {
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			if err := sendAuthToken(conn, "root", pbsHome); err == nil {
				conn.SetDeadline(time.Time{}) // clear deadline
				return conn, "token", nil
			}
			conn.Close()
			log.Printf("[SERVER] Token auth failed, falling back to privileged port")
		}
	}

	// Fallback: connect from privileged port (legacy, Linux root only)
	conn, err := dialPrivileged(addr)
	if err != nil {
		return nil, "", err
	}
	return conn, "privileged-port", nil
}

// dialPrivileged connects to addr from a privileged local port (< 1024).
// This is the legacy authentication method for PBS server.
func dialPrivileged(addr string) (net.Conn, error) {
for port := 1023; port >= 600; port-- {
localAddr := &net.TCPAddr{Port: port}
d := net.Dialer{
LocalAddr: localAddr,
Timeout:   10 * time.Second,
}
conn, err := d.Dial("tcp", addr)
if err == nil {
return conn, nil
}
if !strings.Contains(err.Error(), "address already in use") {
return nil, err
}
}
// Fall back to any port
return net.DialTimeout("tcp", addr, 10*time.Second)
}

const (
	// BatchReqAuthToken is the request type for token-based authentication.
	batchReqAuthToken = 63
	// authKeyFileName is the shared secret key file name.
	authKeyFileName = "auth_key"
)

// sendAuthToken sends an AuthToken request on the connection to authenticate.
// Returns nil if the server accepted the token.
func sendAuthToken(conn net.Conn, user string, pbsHome string) error {
	// Load shared key
	keyPath := filepath.Join(pbsHome, authKeyFileName)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("read auth key %s: %w", keyPath, err)
	}
	hexStr := strings.TrimSpace(string(data))
	key, err := hex.DecodeString(hexStr)
	if err != nil {
		return fmt.Errorf("decode auth key: %w", err)
	}

	// Compute HMAC token
	timestamp := time.Now().Unix()
	message := fmt.Sprintf("%s|%d", user, timestamp)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	token := hex.EncodeToString(mac.Sum(nil))

	// Send AuthToken request: header + body(timestamp, token) + extension
	w := dis.NewWriter(conn)
	// Header: proto=2, ver=2, type=63, user
	if err := w.WriteUint(2); err != nil {
		return err
	}
	if err := w.WriteUint(2); err != nil {
		return err
	}
	if err := w.WriteUint(batchReqAuthToken); err != nil {
		return err
	}
	if err := w.WriteString(user); err != nil {
		return err
	}
	// Body: timestamp + token
	if err := w.WriteUint(uint64(timestamp)); err != nil {
		return err
	}
	if err := w.WriteString(token); err != nil {
		return err
	}
	// Extension: empty
	if err := w.WriteString(""); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}

	// Read reply
	r := dis.NewReader(conn)
	r.ReadUint() // proto
	r.ReadUint() // ver
	code, err := r.ReadInt()
	if err != nil {
		return fmt.Errorf("read auth reply: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("auth rejected with code %d", code)
	}

	return nil
}
