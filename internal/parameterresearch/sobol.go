package parameterresearch

import (
	"fmt"
	"math"
	"math/bits"
)

type sobolPolynomial struct {
	s int
	a uint32
	m []uint32
}

// The first 20 Joe-Kuo direction-number rows cover the current strategy and
// dynamic-policy research dimensions. Keeping them versioned makes continuation
// deterministic across service restarts.
var sobolPolynomials = []sobolPolynomial{
	{},
	{1, 0, []uint32{1}},
	{2, 1, []uint32{1, 3}},
	{3, 1, []uint32{1, 3, 1}},
	{3, 2, []uint32{1, 1, 1}},
	{4, 1, []uint32{1, 3, 5, 13}},
	{4, 4, []uint32{1, 1, 5, 5}},
	{5, 2, []uint32{1, 3, 3, 9, 7}},
	{5, 4, []uint32{1, 3, 7, 13, 3}},
	{5, 7, []uint32{1, 1, 5, 11, 27}},
	{5, 11, []uint32{1, 1, 7, 3, 29}},
	{5, 13, []uint32{1, 3, 7, 7, 21}},
	{5, 14, []uint32{1, 3, 1, 9, 23}},
	{6, 1, []uint32{1, 3, 3, 5, 19, 33}},
	{6, 13, []uint32{1, 1, 3, 13, 11, 7}},
	{6, 16, []uint32{1, 1, 7, 13, 25, 5}},
	{6, 19, []uint32{1, 3, 5, 11, 7, 11}},
	{6, 22, []uint32{1, 1, 1, 3, 13, 39}},
	{6, 25, []uint32{1, 3, 1, 15, 17, 63}},
	{6, 28, []uint32{1, 1, 5, 5, 1, 27}},
}

func SobolPoint(index uint64, dimensions int) ([]float64, error) {
	if dimensions < 1 || dimensions > len(sobolPolynomials) || index > math.MaxUint32-1 {
		return nil, fmt.Errorf("%w: Sobol 維度或索引超出版本上限", ErrInvalidPlan)
	}
	gray := uint32(index ^ (index >> 1))
	result := make([]float64, dimensions)
	for dimension := 0; dimension < dimensions; dimension++ {
		directions := sobolDirections(dimension)
		x := uint32(0)
		value := gray
		for value != 0 {
			bit := bits.TrailingZeros32(value)
			x ^= directions[bit]
			value &= value - 1
		}
		result[dimension] = float64(x) / 4294967296.0
	}
	return result, nil
}

func sobolDirections(dimension int) [32]uint32 {
	var directions [32]uint32
	if dimension == 0 {
		for j := 0; j < 32; j++ {
			directions[j] = uint32(1) << (31 - j)
		}
		return directions
	}
	p := sobolPolynomials[dimension]
	for j := 1; j <= p.s; j++ {
		directions[j-1] = p.m[j-1] << (32 - j)
	}
	for j := p.s + 1; j <= 32; j++ {
		value := directions[j-p.s-1] ^ (directions[j-p.s-1] >> p.s)
		for k := 1; k <= p.s-1; k++ {
			if ((p.a >> (p.s - 1 - k)) & 1) == 1 {
				value ^= directions[j-k-1]
			}
		}
		directions[j-1] = value
	}
	return directions
}
