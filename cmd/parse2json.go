package main

import (
	"blowing-simulator/internal/simulator"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
)

func main() {
	// CLI flags
	inputPath := flag.String("in", "", "Path to normalized TXT file (Jetting or Fremco)")
	format := flag.String("format", "", "Force format: 'jetting' or 'fremco' (optional, auto-detect if empty)")
	outPath := flag.String("out", "", "Path to output JSON file (optional, prints to stdout if empty)")
	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: parse2json -in <normalized.txt> [-format jetting|fremco] [-out output.json]")
		os.Exit(1)
	}

	raw, err := ioutil.ReadFile(*inputPath)
	if err != nil {
		log.Fatalf("Failed to read input file: %v", err)
	}
	rawStr := string(raw)

	var measurements []simulator.SimpleMeasurement
	// Auto-detect format if not forced
	if *format == "" {
		if strings.Contains(rawStr, "Streckenlänge") && strings.Contains(rawStr, "Drehmoment") {
			*format = "fremco"
		} else if strings.Contains(rawStr, "Länge") && strings.Contains(rawStr, "Schubkraft") {
			*format = "jetting"
		}
	}

	switch *format {
	case "fremco":
		measurements = simulator.ParseFremcoSimple(rawStr)
	case "jetting":
		measurements = simulator.ParseJettingTxt(rawStr)
	default:
		fmt.Fprintln(os.Stderr, "Could not auto-detect format. Please specify -format jetting or fremco.")
		os.Exit(2)
	}

	jsonOutput, err := json.MarshalIndent(measurements, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON: %v", err)
	}

	if *outPath == "" {
		fmt.Println(string(jsonOutput))
	} else {
		err = ioutil.WriteFile(*outPath, jsonOutput, 0644)
		if err != nil {
			log.Fatalf("Failed to write output file: %v", err)
		}
		fmt.Printf("JSON written to %s\n", *outPath)
	}
}
