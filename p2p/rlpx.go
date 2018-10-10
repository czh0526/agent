package p2p

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/czh0526/agent/log"
	"github.com/czh0526/agent/p2p/discover"
	"github.com/czh0526/agent/rlp"
	"github.com/czh0526/agent/crypto"
	"github.com/czh0526/agent/crypto/ecies"
)

const (
	sskLen = 16
	sigLen = 65
	pubLen = 64
	shaLen = 32

	authMsgLen  = pubLen + 1
	authRespLen = pubLen + 1
)

type rlpx struct {
	fd net.Conn
}

func newRLPX(fd net.Conn) transport {
	return &rlpx{
		fd: fd,
	}
}

type secrets struct {
	RemoteID discover.NodeID
}

type encHandshake struct {
	initiator bool
	remoteID  discover.NodeID
	remotePub *ecies.PublicKey
}

type authMsgV4 struct {
	gotPlain        bool
	InitiatorPubkey [pubLen]byte
	Version         uint
}

type authRespV4 struct {
	ReceiverPubkey [pubLen]byte
	Version        uint
}

type plainDecoder interface {
	decodePlain([]byte)
}

func (msg *authMsgV4) sealPlain(h *encHandshake) ([]byte, error) {
	rpub, err := rlp.EncodeToBytes(msg)
	if err != nil {
		return []byte{}, err
	}
	buf := make([]byte, len(rpub)+2)
	binary.BigEndian.PutUint16(buf[:], uint16(len(rpub)))
	copy(buf[2:], rpub)
	return buf, nil
}

func (msg *authMsgV4) decodePlain(input []byte) {
	copy(msg.InitiatorPubkey[:], input)
	msg.Version = 4
	msg.gotPlain = true
}

func (msg *authRespV4) sealPlain(h *encHandshake) ([]byte, error) {
	rpub, err := rlp.EncodeToBytes(msg)
	if err != nil {
		return []byte{}, err
	}
	buf := make([]byte, len(rpub)+2)
	binary.BigEndian.PutUint16(buf[:], uint16(len(rpub)))
	copy(buf[2:], rpub)
	return buf, nil
}

func (msg *authRespV4) decodePlain(input []byte) {
	copy(msg.ReceiverPubkey[:], input)
	msg.Version = 4
}

func (h *encHandshake) makeAuthMsg(prv *ecdsa.PrivateKey) (*authMsgV4, error) {
	rpub, err := h.remoteID.Pubkey()
	if err != nil {
		return nil, fmt.Errorf("bad remoteID: %v", err)
	}
	h.remotePub = ecies.ImportECDSAPublic(rpub)

	msg := new(authMsgV4)
	msg.Version = 4
	copy(msg.InitiatorPubkey[:], crypto.FromECDSAPub(&prv.PublicKey)[1:])
	return msg, nil
}

func (h *encHandshake) makeAuthResp(prv *ecdsa.PrivateKey) (msg *authRespV4, err error) {
	msg = new(authRespV4)
	msg.Version = 4
	copy(msg.ReceiverPubkey[:], crypto.FromECDSAPub(&prv.PublicKey)[1:])
	return msg, nil
}

func (t *rlpx) doEncHandshake(prv *ecdsa.PrivateKey, dial *discover.Node) (discover.NodeID, error) {
	var (
		sec secrets
		err error
	)
	if dial == nil {
		sec, err = receiverEncHandshake(t.fd, prv, nil)
	} else {
		sec, err = initiatorEncHandshake(t.fd, prv, dial.ID, nil)
	}
	if err != nil {
		return discover.NodeID{}, err
	}

	return sec.RemoteID, nil
}

func initiatorEncHandshake(conn io.ReadWriter, prv *ecdsa.PrivateKey, remoteID discover.NodeID, token []byte) (s secrets, err error) {
	log.Debug("initiatorEncHandshake()")
	// 构造 handshake 对象
	h := &encHandshake{initiator: true, remoteID: remoteID}

	// 构造 auth 消息
	authMsg, err := h.makeAuthMsg(prv)
	if err != nil {
		return s, err
	}
	// 封装 auth 消息： authMsgV4 ==> []byte
	authPacket, err := authMsg.sealPlain(h)
	if err != nil {
		return s, err
	}

	// 发送消息
	var n = 0
	if n, err = conn.Write(authPacket); err != nil {
		return s, err
	}
	log.Debug("write msg", "n", n)

	// 接收 authRespV4
	authRespMsg := new(authRespV4)
	if err := readHandshakeMsg(authRespMsg, conn); err != nil {
		return s, err
	}
	// 处理 authRespV4
	if err := h.handleAuthResp(authRespMsg); err != nil {
		return s, err
	}

	return h.secrets(authMsg, authRespMsg)
}

func receiverEncHandshake(conn io.ReadWriter, prv *ecdsa.PrivateKey, token []byte) (s secrets, err error) {
	log.Debug("receiverEncHandshake() ")
	authMsg := new(authMsgV4)
	if err := readHandshakeMsg(authMsg, conn); err != nil {
		return s, err
	}
	log.Debug("read handshake msg")

	h := new(encHandshake)
	if err := h.handleAuthMsg(authMsg, prv); err != nil {
		return s, err
	}
	log.Debug("handle auth msg")

	authRespMsg, err := h.makeAuthResp(prv)
	if err != nil {
		return s, err
	}
	var authRespPacket []byte
	authRespPacket, err = authRespMsg.sealPlain(h)
	if err != nil {
		return s, err
	}
	log.Debug("seal plain auth resp ")

	if _, err = conn.Write(authRespPacket); err != nil {
		return s, err
	}
	log.Debug("send auth resp msg.")

	return h.secrets(authMsg, authRespMsg)
}

func readHandshakeMsg(msg plainDecoder, r io.Reader) error {
	log.Debug("read prefix ... ")
	prefix := make([]byte, 2)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return err
	}

	size := binary.BigEndian.Uint16(prefix)
	log.Debug(fmt.Sprintf("read %v bytes message ...", size))

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	log.Debug("read message into buf ... ")

	s := rlp.NewStream(bytes.NewReader(buf), 0)
	return s.Decode(msg)
}

func (h *encHandshake) handleAuthMsg(msg *authMsgV4, prv *ecdsa.PrivateKey) error {
	h.remoteID = msg.InitiatorPubkey
	rpub, err := h.remoteID.Pubkey()
	if err != nil {
		return fmt.Errorf("bad remoteID: %#v", err)
	}
	h.remotePub = ecies.ImportECDSAPublic(rpub)
	return nil
}

func (h *encHandshake) handleAuthResp(msg *authRespV4) (err error) {
	h.remoteID = msg.ReceiverPubkey
	rpub, err := h.remoteID.Pubkey()
	if err != nil {
		return fmt.Errorf("bad remoteID: %#v", err)
	}
	h.remotePub = ecies.ImportECDSAPublic(rpub)
	return nil
}

func (h *encHandshake) secrets(authMsg *authMsgV4, authRespMsg *authRespV4) (secrets, error) {
	s := secrets{
		RemoteID: h.remoteID,
	}

	return s, nil
}
