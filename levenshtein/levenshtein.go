package levenshtein //nolint:stylecheck,revive // copied code

import "sync"

// This is copied from https://github.com/ka-weihe/fast-levenshtein/blob/main/levenshtein.go,
// modified for concurrency safety.

const (
	peqSize    = 0x10000
	phcMhcSize = 256
	uintsSize  = peqSize + phcMhcSize*2
)

var uint64sPool = sync.Pool{
	New: func() any {
		return &[uintsSize]uint64{}
	},
}

//nolint:wsl,varnamelen // copied code
func m64(a []rune, b []rune, uint64s *[uintsSize]uint64) int {
	peq := uint64s[:peqSize]

	pv := ^uint64(0)
	mv := uint64(0)
	sc := 0
	for _, c := range a {
		peq[c] |= uint64(1) << sc
		sc++
	}
	ls := uint64(1) << (sc - 1)
	for _, c := range b {
		eq := peq[c]
		xv := eq | mv
		eq |= ((eq & pv) + pv) ^ pv
		mv |= ^(eq | pv)
		pv &= eq
		if (mv & ls) != 0 {
			sc++
		}
		if (pv & ls) != 0 {
			sc--
		}
		mv = (mv << 1) | 1
		pv = (pv << 1) | ^(xv | mv)
		mv &= xv
	}
	for _, c := range a {
		peq[c] = 0
	}
	return sc
}

//nolint:wsl // copied code
func min(x int, y int) int {
	if x < y {
		return x
	}
	return y
}

//nolint:wsl,gocognit,cyclop,varnamelen // copied code
func mx(s1 []rune, s2 []rune, uint64s *[uintsSize]uint64) int {
	peq := uint64s[:peqSize]
	phc := uint64s[peqSize : peqSize+phcMhcSize]
	mhc := uint64s[peqSize+phcMhcSize:]

	n := len(s1)
	m := len(s2)
	hsize := 1 + ((n - 1) / 64)
	vsize := 1 + ((m - 1) / 64)
	for i := 0; i < hsize; i++ {
		phc[i] = ^uint64(0)
		mhc[i] = 0
	}
	j := 0
	for ; j < vsize-1; j++ {
		mv := uint64(0)
		pv := ^uint64(0)
		start := j * 64
		vlen := min(64, m) + start
		for k := start; k < vlen; k++ {
			peq[s2[k]] |= uint64(1) << (k & 63)
		}

		for i := 0; i < n; i++ {
			eq := peq[s1[i]]
			pb := (phc[i/64] >> (i & 63)) & 1
			mb := (mhc[i/64] >> (i & 63)) & 1
			xv := eq | mv
			xh := ((((eq | mb) & pv) + pv) ^ pv) | eq | mb
			ph := mv | ^(xh | pv)
			mh := pv & xh
			if ((ph >> 63) ^ pb) != 0 {
				phc[i/64] ^= uint64(1) << (i & 63)
			}
			if ((mh >> 63) ^ mb) != 0 {
				mhc[i/64] ^= uint64(1) << (i & 63)
			}
			ph = (ph << 1) | pb
			mh = (mh << 1) | mb
			pv = mh | ^(xv | ph)
			mv = ph & xv
		}
		for k := start; k < vlen; k++ {
			peq[s2[k]] = 0
		}
	}
	mv := uint64(0)
	pv := ^uint64(0)
	start := j * 64
	vlen := min(64, m-start) + start
	for k := start; k < vlen; k++ {
		peq[s2[k]] |= uint64(1) << (k & 63)
	}
	sc := uint64(m)
	for i := 0; i < n; i++ {
		eq := peq[s1[i]]
		pb := (phc[i/64] >> (i & 63)) & 1
		mb := (mhc[i/64] >> (i & 63)) & 1
		xv := eq | mv
		xh := ((((eq | mb) & pv) + pv) ^ pv) | eq | mb
		ph := mv | ^(xh | pv)
		mh := pv & xh
		sc += (ph >> ((m - 1) & 63)) & 1
		sc -= (mh >> ((m - 1) & 63)) & 1
		if ((ph >> 63) ^ pb) != 0 {
			phc[i/64] ^= uint64(1) << (i & 63)
		}
		if ((mh >> 63) ^ mb) != 0 {
			mhc[i/64] ^= uint64(1) << (i & 63)
		}
		ph = (ph << 1) | pb
		mh = (mh << 1) | mb
		pv = mh | ^(xv | ph)
		mv = ph & xv
	}
	for k := start; k < vlen; k++ {
		peq[s2[k]] = 0
	}
	return int(sc)
}

//nolint:varnamelen,revive // copied code
func Distance(a []rune, b []rune) int {
	if len(a) < len(b) {
		a, b = b, a
	}

	if len(b) == 0 {
		return len(a)
	}

	uint64s := uint64sPool.Get().(*[uintsSize]uint64) //nolint:forcetypeassert // we know what's in the pool
	defer uint64sPool.Put(uint64s)

	if len(a) <= 64 {
		return m64(a, b, uint64s)
	}

	return mx(a, b, uint64s)
}
