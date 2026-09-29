package core

import (
	"image/color"
	"math"
	"math/rand"
)

func ToRGB(c color.Color) RGB {
	r, g, b, _ := c.RGBA()
	r5 := (r >> 8) >> 3
	g5 := (g >> 8) >> 3
	b5 := (b >> 8) >> 3
	return RGB{
		R: float64(r5 << 3),
		G: float64(g5 << 3),
		B: float64(b5 << 3),
	}
}

func ToRGBFull(c color.Color) RGB {
	r, g, b, _ := c.RGBA()
	return RGB{
		R: float64(r >> 8),
		G: float64(g >> 8),
		B: float64(b >> 8),
	}
}

func RoundRGB555(c RGB) RGB {
	r5 := func(v float64) float64 {
		v5 := math.Round(v / 8)
		if v5 < 0 {
			v5 = 0
		} else if v5 > 31 {
			v5 = 31
		}
		return v5 * 8
	}
	return RGB{R: r5(c.R), G: r5(c.G), B: r5(c.B)}
}

func ColorDist(a, b RGB) float64 {
	dr := a.R - b.R
	dg := a.G - b.G
	db := a.B - b.B
	return float64(dr*dr + dg*dg + db*db)
}

func NearestIdx(c RGB, palette []RGB) int {
	best, bestDist := 0, math.MaxFloat64
	for i, p := range palette {
		if d := ColorDist(c, p); d < bestDist {
			bestDist = d
			best = i
		}
	}
	return best
}

func KMeans(points []RGB, k int, iters int) []RGB {
	if len(points) == 0 || k <= 0 {
		out := make([]RGB, k)
		for i := range out {
			v := float64(i) / math.Max(float64(k-1), 1) * 255
			out[i] = RGB{v, v, v}
		}
		return out
	}
	if k > len(points) {
		k = len(points)
	}
	centers := make([]RGB, 0, k)

	centers = append(centers, points[rand.Intn(len(points))])

	for len(centers) < k {
		dists := make([]float64, len(points))
		total := 0.0
		for i, p := range points {
			best := math.MaxFloat64
			for _, c := range centers {
				if d := ColorDist(p, c); d < best {
					best = d
				}
			}
			dists[i] = best
			total += best
		}

		r := rand.Float64() * total
		cumsum := 0.0
		for i, d := range dists {
			cumsum += d
			if cumsum >= r {
				centers = append(centers, points[i])
				break
			}
		}
	}

	labels := make([]int, len(points))
	for iter := 0; iter < iters; iter++ {
		for i, p := range points {
			labels[i] = NearestIdx(p, centers)
		}
		sums := make([]RGB, k)
		counts := make([]int, k)
		for i, p := range points {
			l := labels[i]
			sums[l].R += p.R
			sums[l].G += p.G
			sums[l].B += p.B
			counts[l]++
		}
		for i := range centers {
			if counts[i] == 0 {
				best, bestDist := 0, 0.0
				for j, p := range points {
					nearDist := math.MaxFloat64
					for ci, c := range centers {
						if ci == i {
							continue
						}
						if d := ColorDist(p, c); d < nearDist {
							nearDist = d
						}
					}
					if nearDist > bestDist {
						bestDist = nearDist
						best = j
					}
				}
				centers[i] = points[best]
				continue
			}
			centers[i] = RGB{
				R: sums[i].R / float64(counts[i]),
				G: sums[i].G / float64(counts[i]),
				B: sums[i].B / float64(counts[i]),
			}
		}
	}
	return centers
}
