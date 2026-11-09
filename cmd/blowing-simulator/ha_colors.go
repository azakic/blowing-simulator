package main

import (
	"fmt"
	"html/template"
	"strings"
)

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

// findHaColor nalazi opciju po ključu.
func findHaColor(list []haColorOption, key string) *haColorOption {
	for i := range list {
		if list[i].Key == key {
			return &list[i]
		}
	}
	return nil
}

// selectedHAFibers vraća izabrane boje u redosledu palete.
func selectedHAFibers(keys string) []haColorOption {
	sel := map[string]bool{}
	for _, k := range strings.Split(keys, ",") {
		sel[strings.TrimSpace(k)] = true
	}
	out := make([]haColorOption, 0, len(haFiberColors))
	for _, c := range haFiberColors {
		if sel[c.Key] {
			out = append(out, haColorOption{Key: c.Key, Label: c.Label, Hex: c.Hex})
		}
	}
	return out
}

// haSwatchStyle pravi CSS pozadinu za swatch (puna boja ili bela pruga).
func haSwatchStyle(v any) template.CSS {
	var c haColorOption
	switch x := v.(type) {
	case haColorOption:
		c = x
	case *haColorOption:
		if x == nil {
			return ""
		}
		c = *x
	default:
		return ""
	}
	rgb := fmt.Sprintf("rgb(%d,%d,%d)", c.Hex[0], c.Hex[1], c.Hex[2])
	if c.Striped {
		return template.CSS(fmt.Sprintf("background:linear-gradient(135deg,%s 0 40%%,#fff 40%% 60%%,%s 60%% 100%%)", rgb, rgb))
	}
	return template.CSS("background:" + rgb)
}
