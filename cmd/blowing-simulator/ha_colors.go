package main

// haColorOption je jedna boja u obrascu. Striped označava varijantu sa belom
// prugom (Röhrchenfarbe: 12 punih + 12 sa belom prugom = 24).
type haColorOption struct {
	Key     string
	Label   string
	Hex     [3]int
	Striped bool
}

// Rohrfarbe: 5 boja koje se nude za polje Rohr (1–4).
var haRohrColors = []haColorOption{
	{Key: "orange", Label: "orange", Hex: [3]int{255, 109, 0}},
	{Key: "blau", Label: "blau", Hex: [3]int{0, 80, 160}},
	{Key: "gruen", Label: "grün", Hex: [3]int{0, 150, 64}},
	{Key: "violett", Label: "violett", Hex: [3]int{123, 31, 162}},
	{Key: "gelb", Label: "gelb", Hex: [3]int{255, 215, 0}},
}

// Röhrchenfarbe: istih 12 boja kao „Genutzte Faserfarben“, plus iste sa belom
// prugom — ukupno 24.
var haRoehrchenColors = func() []haColorOption {
	out := make([]haColorOption, 0, 24)
	for _, c := range haFiberColors {
		out = append(out, haColorOption{Key: c.Key, Label: c.Label, Hex: c.Hex})
	}
	for _, c := range haFiberColors {
		out = append(out, haColorOption{Key: c.Key + "-weiss", Label: c.Label + "/weiß", Hex: c.Hex, Striped: true})
	}
	return out
}()
