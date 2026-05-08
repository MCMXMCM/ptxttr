package bloom

import (
	"hash/fnv"
	"math"
)

const defaultFalsePositiveRate = 0.01

type Filter struct {
	bits []byte
	m    uint64
	k    uint64
}

func New(capacity int) *Filter {
	if capacity < 1 {
		capacity = 1
	}
	mBits := optimalBits(capacity, defaultFalsePositiveRate)
	if mBits < 64 {
		mBits = 64
	}
	kHashes := optimalHashes(capacity, mBits)
	if kHashes < 2 {
		kHashes = 2
	}
	return &Filter{
		bits: make([]byte, (mBits+7)/8),
		m:    uint64(mBits),
		k:    uint64(kHashes),
	}
}

func (f *Filter) Add(value string) {
	if f == nil || f.m == 0 || value == "" {
		return
	}
	h1, h2 := hashes(value)
	for i := uint64(0); i < f.k; i++ {
		f.setBit((h1 + i*h2) % f.m)
	}
}

func (f *Filter) Test(value string) bool {
	if f == nil || f.m == 0 || value == "" {
		return false
	}
	h1, h2 := hashes(value)
	for i := uint64(0); i < f.k; i++ {
		if !f.bitSet((h1 + i*h2) % f.m) {
			return false
		}
	}
	return true
}

func (f *Filter) setBit(index uint64) {
	byteIndex := index / 8
	bitMask := byte(1 << (index % 8))
	f.bits[byteIndex] |= bitMask
}

func (f *Filter) bitSet(index uint64) bool {
	byteIndex := index / 8
	bitMask := byte(1 << (index % 8))
	return f.bits[byteIndex]&bitMask != 0
}

// hashes derives two independent 64-bit hashes from value via FNV-1a with
// distinct seeds (the second seed is XOR'd into the basis). A non-cryptographic
// hash is sufficient here: the filter is built from short-lived author lists,
// not adversarial input, and FNV is roughly an order of magnitude cheaper than
// SHA-256 in the per-event Test() hot path.
func hashes(value string) (uint64, uint64) {
	h1 := fnv.New64a()
	_, _ = h1.Write([]byte(value))
	h2 := fnv.New64a()
	_, _ = h2.Write([]byte{0x9e, 0x37, 0x79, 0xb9})
	_, _ = h2.Write([]byte(value))
	a := h1.Sum64()
	b := h2.Sum64()
	if b == 0 {
		b = 0x27d4eb2d
	}
	return a, b
}

func optimalBits(capacity int, falsePositiveRate float64) int {
	return int(math.Ceil((-float64(capacity) * math.Log(falsePositiveRate)) / (math.Ln2 * math.Ln2)))
}

func optimalHashes(capacity int, bits int) int {
	return int(math.Round((float64(bits) / float64(capacity)) * math.Ln2))
}
