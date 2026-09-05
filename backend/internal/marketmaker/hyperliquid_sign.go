package marketmaker

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// hyperliquid_sign.go implements Hyperliquid's L1 action signing. Verified against
// the official Python SDK's test vectors (see hyperliquid_sign_test.go) — those
// pin the whole chain byte-for-byte, which matters because ANY deviation in the
// msgpack bytes silently produces a valid-looking signature the exchange rejects.
//
// Algorithm (hyperliquid-python-sdk/hyperliquid/utils/signing.py):
//  1. data = msgpack(action) || nonce(8B big-endian) || 0x00            (no vault)
//                                                    || 0x01 || vault(20B)
//     (+ optional 0x00 || expiresAfter(8B) — not used here)
//  2. connectionId = keccak256(data)
//  3. EIP-712 sign {source: "a"(mainnet)|"b"(testnet), connectionId} under
//     domain{name:"Exchange", version:"1", chainId:1337, verifyingContract:0x0}
//
// We hand-roll msgpack rather than pulling a generic library because Python's
// msgpack.packb serialises dict keys in INSERTION order while most Go libraries
// sort them — a mismatch there changes the hash and every request 401s.

// mpValue is an ordered msgpack value. Only the subset Hyperliquid actions use is
// supported: ordered map, array, string, uint/int, bool.
type mpValue interface{ encodeMsgpack(*[]byte) }

// mpMap preserves field order (Python dict insertion order); never sorts.
type mpMap struct {
	keys []string
	vals []mpValue
}

func newMap() *mpMap { return &mpMap{} }

func (m *mpMap) set(k string, v mpValue) *mpMap {
	m.keys = append(m.keys, k)
	m.vals = append(m.vals, v)
	return m
}
func (m *mpMap) setStr(k, v string) *mpMap       { return m.set(k, mpStr(v)) }
func (m *mpMap) setInt(k string, v int64) *mpMap { return m.set(k, mpInt(v)) }
func (m *mpMap) setBool(k string, v bool) *mpMap { return m.set(k, mpBool(v)) }

func (m *mpMap) encodeMsgpack(out *[]byte) {
	n := len(m.keys)
	switch {
	case n <= 15:
		*out = append(*out, byte(0x80|n))
	case n <= 0xFFFF:
		*out = append(*out, 0xde, byte(n>>8), byte(n))
	default:
		*out = append(*out, 0xdf, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	for i, k := range m.keys {
		mpStr(k).encodeMsgpack(out)
		m.vals[i].encodeMsgpack(out)
	}
}

type mpArr []mpValue

func (a mpArr) encodeMsgpack(out *[]byte) {
	n := len(a)
	switch {
	case n <= 15:
		*out = append(*out, byte(0x90|n))
	case n <= 0xFFFF:
		*out = append(*out, 0xdc, byte(n>>8), byte(n))
	default:
		*out = append(*out, 0xdd, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	for _, v := range a {
		v.encodeMsgpack(out)
	}
}

type mpStr string

func (s mpStr) encodeMsgpack(out *[]byte) {
	b := []byte(s)
	n := len(b)
	switch {
	case n <= 31:
		*out = append(*out, byte(0xa0|n))
	case n <= 0xFF:
		*out = append(*out, 0xd9, byte(n))
	case n <= 0xFFFF:
		*out = append(*out, 0xda, byte(n>>8), byte(n))
	default:
		*out = append(*out, 0xdb, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	*out = append(*out, b...)
}

type mpInt int64

func (i mpInt) encodeMsgpack(out *[]byte) {
	v := int64(i)
	if v >= 0 {
		switch {
		case v <= 0x7F:
			*out = append(*out, byte(v))
		case v <= 0xFF:
			*out = append(*out, 0xcc, byte(v))
		case v <= 0xFFFF:
			*out = append(*out, 0xcd, byte(v>>8), byte(v))
		case v <= 0xFFFFFFFF:
			*out = append(*out, 0xce, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		default:
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			*out = append(*out, 0xcf)
			*out = append(*out, b[:]...)
		}
		return
	}
	switch {
	case v >= -32:
		*out = append(*out, byte(0xe0|(v+32)))
	case v >= -128:
		*out = append(*out, 0xd0, byte(v))
	case v >= -32768:
		*out = append(*out, 0xd1, byte(v>>8), byte(v))
	case v >= -2147483648:
		*out = append(*out, 0xd2, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(v))
		*out = append(*out, 0xd3)
		*out = append(*out, b[:]...)
	}
}

type mpBool bool

func (b mpBool) encodeMsgpack(out *[]byte) {
	if b {
		*out = append(*out, 0xc3)
	} else {
		*out = append(*out, 0xc2)
	}
}

func keccak256(parts ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// hlActionHash = keccak256(msgpack(action) || nonce_be64 || vault marker).
func hlActionHash(action mpValue, nonce uint64, vaultAddress string) []byte {
	var data []byte
	action.encodeMsgpack(&data)
	var nb [8]byte
	binary.BigEndian.PutUint64(nb[:], nonce)
	data = append(data, nb[:]...)
	if strings.TrimSpace(vaultAddress) == "" {
		data = append(data, 0x00)
	} else {
		data = append(data, 0x01)
		data = append(data, hexToBytes(vaultAddress)...)
	}
	return keccak256(data)
}

func hexToBytes(s string) []byte {
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "0x")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil
		}
		out[i] = byte(v)
	}
	return out
}

// EIP-712 constants for the L1 "Agent" phantom message.
var (
	hlDomainTypeHash = keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	hlAgentTypeHash  = keccak256([]byte("Agent(string source,bytes32 connectionId)"))
	// domain: name="Exchange", version="1", chainId=1337, verifyingContract=0x0
	hlDomainSeparator = keccak256(
		hlDomainTypeHash,
		keccak256([]byte("Exchange")),
		keccak256([]byte("1")),
		leftPad32(big.NewInt(1337).Bytes()),
		make([]byte, 32),
	)
)

func leftPad32(b []byte) []byte {
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// hlSigningHash builds the EIP-712 digest: keccak256(0x19 0x01 || domain || structHash).
func hlSigningHash(connectionID []byte, mainnet bool) []byte {
	source := "b"
	if mainnet {
		source = "a"
	}
	structHash := keccak256(hlAgentTypeHash, keccak256([]byte(source)), connectionID)
	return keccak256([]byte{0x19, 0x01}, hlDomainSeparator, structHash)
}

// hlSignature is the {r,s,v} the exchange endpoint expects.
type hlSignature struct {
	R string `json:"r"`
	S string `json:"s"`
	V int    `json:"v"`
}

// hlSignAction signs an L1 action with the account's secp256k1 private key.
func hlSignAction(privHex string, action mpValue, nonce uint64, vaultAddress string, mainnet bool) (hlSignature, error) {
	kb := hexToBytes(privHex)
	if len(kb) != 32 {
		return hlSignature{}, fmt.Errorf("hyperliquid: 私钥必须是 32 字节 hex, got %d", len(kb))
	}
	priv := secp256k1.PrivKeyFromBytes(kb)
	digest := hlSigningHash(hlActionHash(action, nonce, vaultAddress), mainnet)

	// SignCompact gives a recoverable signature: [recid+27][R(32)][S(32)], and it
	// already enforces low-S (EIP-2 canonical), which the exchange requires.
	sig := ecdsa.SignCompact(priv, digest, false)
	if len(sig) != 65 {
		return hlSignature{}, fmt.Errorf("hyperliquid: 签名长度异常 %d", len(sig))
	}
	return hlSignature{
		R: "0x" + trimLeadingZeroHex(sig[1:33]),
		S: "0x" + trimLeadingZeroHex(sig[33:65]),
		V: int(sig[0]), // 27 / 28
	}, nil
}

// trimLeadingZeroHex matches eth_account's to_hex(int): no zero padding, and the
// exchange accepts the shortened form (the SDK's own output is produced this way).
func trimLeadingZeroHex(b []byte) string {
	s := strings.TrimLeft(fmt.Sprintf("%x", b), "0")
	if s == "" {
		return "0"
	}
	return s
}

// hlFloatToWire formats a number the way the SDK's float_to_wire does: fixed to 8
// decimals then trailing zeros stripped ("100" not "100.00000000"). The exact
// string goes into the msgpack payload, so formatting differences break the hash.
func hlFloatToWire(x float64) string {
	s := strconv.FormatFloat(x, 'f', 8, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" || s == "" {
		s = "0"
	}
	return s
}

// hlNormalizePrice enforces Hyperliquid's price rule: at most 5 significant digits,
// and at most (MAX_DECIMALS - szDecimals) decimal places, where MAX_DECIMALS is 6
// for perps. Integers are always allowed regardless of significant figures.
func hlNormalizePrice(px float64, szDecimals int, perp bool) float64 {
	if px <= 0 {
		return 0
	}
	maxDec := 6
	if !perp {
		maxDec = 8
	}
	decimals := maxDec - szDecimals
	if decimals < 0 {
		decimals = 0
	}
	// 5 significant figures
	r, _ := strconv.ParseFloat(strconv.FormatFloat(px, 'g', 5, 64), 64)
	// then clamp decimal places
	r, _ = strconv.ParseFloat(strconv.FormatFloat(r, 'f', decimals, 64), 64)
	return r
}

// sortedKeys is used only by tests/debug dumps; kept out of the hash path.
func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
