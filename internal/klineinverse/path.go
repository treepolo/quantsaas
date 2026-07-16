package klineinverse

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

func Reconstruct(path Path, initialClose float64) ([]OHLC, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	if !finite(initialClose) || initialClose <= 0 {
		return nil, fmt.Errorf("%w：起始收盤價必須為正", ErrInvalidPath)
	}
	bars := make([]OHLC, len(path.Coordinates))
	previous := initialClose
	for index, coordinate := range path.Coordinates {
		open := previous * math.Exp(coordinate.G)
		closeValue := open * math.Exp(coordinate.B)
		high := math.Max(open, closeValue) * math.Exp(coordinate.U)
		low := math.Min(open, closeValue) / math.Exp(coordinate.D)
		if !finite(open) || !finite(closeValue) || !finite(high) || !finite(low) || low <= 0 || high < math.Max(open, closeValue) || low > math.Min(open, closeValue) {
			return nil, fmt.Errorf("%w：第 %d 根重建後 OHLC 不合法", ErrInvalidPath, index)
		}
		bars[index] = OHLC{TimeMs: path.Dates[index], Open: open, High: high, Low: low, Close: closeValue}
		previous = closeValue
	}
	return bars, nil
}

func Coordinates(bars []OHLC, previousClose float64) ([]Coordinate, error) {
	if len(bars) == 0 || !finite(previousClose) || previousClose <= 0 {
		return nil, fmt.Errorf("%w：缺少合法 K 線或前收", ErrInvalidPath)
	}
	coordinates := make([]Coordinate, len(bars))
	for index, bar := range bars {
		if !validOHLC(bar) {
			return nil, fmt.Errorf("%w：第 %d 根 OHLC 不合法", ErrInvalidPath, index)
		}
		coordinate := Coordinate{
			G: math.Log(bar.Open / previousClose),
			B: math.Log(bar.Close / bar.Open),
			U: math.Log(bar.High / math.Max(bar.Open, bar.Close)),
			D: math.Log(math.Min(bar.Open, bar.Close) / bar.Low),
		}
		if err := validateCoordinate(coordinate); err != nil {
			return nil, fmt.Errorf("%w：第 %d 根座標不合法", err, index)
		}
		coordinates[index] = coordinate
		previousClose = bar.Close
	}
	return coordinates, nil
}

func Normalize(coordinate Coordinate, bounds Bounds) (Coordinate, error) {
	if err := bounds.Validate(); err != nil {
		return Coordinate{}, err
	}
	if err := validateCoordinate(coordinate); err != nil {
		return Coordinate{}, err
	}
	if coordinate.G < bounds.GMin || coordinate.G > bounds.GMax ||
		coordinate.B < bounds.BMin || coordinate.B > bounds.BMax ||
		coordinate.U < bounds.UMin || coordinate.U > bounds.UMax ||
		coordinate.D < bounds.DMin || coordinate.D > bounds.DMax {
		return Coordinate{}, fmt.Errorf("%w：座標超出搜尋邊界", ErrInvalidPath)
	}
	return Coordinate{
		G: normalizeChannel(coordinate.G, bounds.GMin, bounds.GMax),
		B: normalizeChannel(coordinate.B, bounds.BMin, bounds.BMax),
		U: normalizeChannel(coordinate.U, bounds.UMin, bounds.UMax),
		D: normalizeChannel(coordinate.D, bounds.DMin, bounds.DMax),
	}, nil
}

func Denormalize(coordinate Coordinate, bounds Bounds) (Coordinate, error) {
	if err := bounds.Validate(); err != nil {
		return Coordinate{}, err
	}
	for _, value := range []float64{coordinate.G, coordinate.B, coordinate.U, coordinate.D} {
		if !finite(value) || value < 0 || value > 1 {
			return Coordinate{}, fmt.Errorf("%w：正規化座標超出 [0,1]", ErrInvalidPath)
		}
	}
	return Coordinate{
		G: bounds.GMin + coordinate.G*(bounds.GMax-bounds.GMin),
		B: bounds.BMin + coordinate.B*(bounds.BMax-bounds.BMin),
		U: bounds.UMin + coordinate.U*(bounds.UMax-bounds.UMin),
		D: bounds.DMin + coordinate.D*(bounds.DMax-bounds.DMin),
	}, nil
}

func Reflect(value float64) (float64, error) {
	if !finite(value) {
		return 0, fmt.Errorf("%w：反射值不是有限數", ErrInvalidPath)
	}
	value = math.Mod(value, 2)
	if value < 0 {
		value += 2
	}
	if value > 1 {
		value = 2 - value
	}
	if value == 0 {
		return 0, nil
	}
	return value, nil
}

func Hash(path Path) (string, error) {
	if err := validatePath(path); err != nil {
		return "", err
	}
	hash := sha256.New()
	hash.Write([]byte(CoordinateVersion))
	buffer := make([]byte, 8)
	binary.BigEndian.PutUint64(buffer, uint64(path.WarmupLength))
	hash.Write(buffer)
	binary.BigEndian.PutUint64(buffer, uint64(path.EvaluationLength))
	hash.Write(buffer)
	for index, coordinate := range path.Coordinates {
		binary.BigEndian.PutUint64(buffer, uint64(path.Dates[index]))
		hash.Write(buffer)
		for _, value := range []float64{coordinate.G, coordinate.B, coordinate.U, coordinate.D} {
			if value == 0 {
				value = 0
			}
			binary.BigEndian.PutUint64(buffer, math.Float64bits(value))
			hash.Write(buffer)
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validatePath(path Path) error {
	if path.WarmupLength < 1 || path.EvaluationLength < 1 || path.WarmupLength+path.EvaluationLength != len(path.Coordinates) || len(path.Dates) != len(path.Coordinates) {
		return fmt.Errorf("%w：W/H、日期與座標長度不一致", ErrInvalidPath)
	}
	for index, coordinate := range path.Coordinates {
		if index > 0 && path.Dates[index] <= path.Dates[index-1] {
			return fmt.Errorf("%w：日期未嚴格遞增", ErrInvalidPath)
		}
		if err := validateCoordinate(coordinate); err != nil {
			return err
		}
	}
	return nil
}

func validateCoordinate(coordinate Coordinate) error {
	for _, value := range []float64{coordinate.G, coordinate.B, coordinate.U, coordinate.D} {
		if !finite(value) {
			return fmt.Errorf("%w：座標含非有限值", ErrInvalidPath)
		}
	}
	if coordinate.U < 0 || coordinate.D < 0 {
		return fmt.Errorf("%w：影線座標不可為負數", ErrInvalidPath)
	}
	return nil
}

func validOHLC(bar OHLC) bool {
	return finite(bar.Open) && finite(bar.High) && finite(bar.Low) && finite(bar.Close) && bar.Open > 0 && bar.Close > 0 && bar.High >= math.Max(bar.Open, bar.Close) && bar.Low > 0 && bar.Low <= math.Min(bar.Open, bar.Close)
}

func normalizeChannel(value, minimum, maximum float64) float64 {
	if maximum == minimum {
		return 0
	}
	return (value - minimum) / (maximum - minimum)
}
