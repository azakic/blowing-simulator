package simulator

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ExtractJettingMetadata scans normalized Jetting TXT for metadata fields.
func ExtractJettingMetadata(normalized string) map[string]string {
	meta := make(map[string]string)
	lines := strings.Split(normalized, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Datum:") {
			meta["Date"] = strings.TrimSpace(strings.TrimPrefix(line, "Datum:"))
		}
		if strings.HasPrefix(line, "Uhrzeit:") {
			meta["Time"] = strings.TrimSpace(strings.TrimPrefix(line, "Uhrzeit:"))
		}
		if strings.HasPrefix(line, "Adresse:") {
			meta["Address"] = strings.TrimSpace(strings.TrimPrefix(line, "Adresse:"))
		}
		if strings.HasPrefix(line, "NVT:") {
			meta["NVT"] = strings.TrimSpace(strings.TrimPrefix(line, "NVT:"))
		}
	}
	return meta
}

// SimpleMeasurement is defined in parse_fremco.go

// Block header detection for Jetting format
func isBlockHeader(line string) bool {
	headers := []string{
		"Streckenlänge [m]", "Geschwindigkeit [m/min]", "Rohr-Druck [bar]", "Drehmoment [%]", "Uhrzeit [hh:mm:ss]",
		"Länge[m]", "Lufttemperatur[°C]", "Schubkraft[N]", "Einblasdruck[bar]", "Geschwindigkeit[m/min]", "Zeit - Dauer[hh:mm:ss]",
	}
	for _, h := range headers {
		if strings.HasPrefix(line, h) {
			return true
		}
	}
	return false
}

// Unit line detection for Jetting format
func isUnitLine(line string) bool {
	units := []string{
		"[m]", "[°C]", "[N]", "[bar]", "[m/min]", "[hh:mm:ss]", "[%]",
	}
	for _, u := range units {
		if strings.HasPrefix(strings.TrimSpace(line), u) {
			return true
		}
	}
	return false
}

// Minimum of multiple ints
func min(vals ...int) int {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals {
		if v < m {
			m = v
		}
	}
	return m
}

// isJettingNumeric checks if a string is a valid number (including decimals)
func isJettingNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// isTimeFormat checks if a string matches time format (hh:mm:ss)
func isTimeFormat(s string) bool {
	timePattern := `^\d{2}:\d{2}:\d{2}$`
	matched, _ := regexp.MatchString(timePattern, s)
	return matched
}

// ParseJettingTxt parses normalized Jetting TXT data and returns a slice of JettingMeasurement.
func ParseJettingTxt(normalized string) []JettingMeasurement {
	lines := strings.Split(normalized, "\n")
	var measurements []JettingMeasurement
	
	// First try: Parse as individual values per line (6 consecutive lines = 1 measurement)
	var valueBuffer []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Skip header lines and company names
		if isBlockHeader(line) || strings.Contains(line, "M.A.X.") || strings.Contains(line, "Bauservice") || strings.Contains(line, "Wolken-ASM") || strings.Contains(line, "GMBH") {
			continue
		}
		
		// Collect numeric values and time values
		if isJettingNumeric(line) || isTimeFormat(line) {
			valueBuffer = append(valueBuffer, line)
			
			// When we have 6 values, create a measurement
			if len(valueBuffer) == 6 {
				length, _ := strconv.ParseFloat(valueBuffer[0], 64)    // Länge[m]
				temp, _ := strconv.ParseFloat(valueBuffer[1], 64)      // Lufttemperatur[°C]
				force, _ := strconv.ParseFloat(valueBuffer[2], 64)     // Schubkraft[N]
				pressure, _ := strconv.ParseFloat(valueBuffer[3], 64)  // Einblasdruck[bar] 
				speed, _ := strconv.ParseFloat(valueBuffer[4], 64)     // Geschwindigkeit[m/min]
				time := valueBuffer[5]                                 // Zeit - Dauer[hh:mm:ss]
				
				measurements = append(measurements, JettingMeasurement{
					Length:      length,
					Temperature: temp,
					Force:       force,
					Pressure:    pressure,
					Speed:       speed,
					Time:        time,
				})
				valueBuffer = []string{} // Reset buffer
			}
			continue
		}
		
		// Second try: Parse as fields in same line (legacy support)
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			// Only parse lines that start with a valid number (skip text headers)
			if length, err := strconv.ParseFloat(fields[0], 64); err == nil {
				temp, _ := strconv.ParseFloat(fields[1], 64)     
				force, _ := strconv.ParseFloat(fields[2], 64)    
				pressure, _ := strconv.ParseFloat(fields[3], 64) 
				speed, _ := strconv.ParseFloat(fields[4], 64)    
				time := fields[5]                                
				measurements = append(measurements, JettingMeasurement{
					Length:      length,
					Temperature: temp,
					Force:       force,
					Pressure:    pressure,
					Speed:       speed,
					Time:        time,
				})
			}
		}
	}
	
	// If no measurements found with individual line parsing, try vertical block format
	if len(measurements) == 0 {
		measurements = parseVerticalBlocks(normalized)
	}
	return measurements
}

// parseVerticalBlocks parses the vertical block format where each column header is followed by all its values
func parseVerticalBlocks(normalized string) []JettingMeasurement {
	lines := strings.Split(normalized, "\n")
	
	// Find the data blocks for each column
	var lengthValues, tempValues, forceValues, pressureValues, speedValues, timeValues []string
	
	currentBlock := ""
	collectingData := false
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// Skip company header
		if strings.HasPrefix(line, "M.A.X.") || strings.Contains(line, "Bauservice") || strings.Contains(line, "Wolken-ASM") || strings.Contains(line, "GMBH") {
			continue
		}
		
		// Detect which block we're in by flexible header match
		isHeader := false
		if strings.Contains(line, "Länge[m]") || strings.Contains(line, "Laenge[m]") {
			println("DEBUG: Found length header:", line)
			currentBlock = "length"
			collectingData = true
			isHeader = true
		} else if strings.Contains(line, "Lufttemperatur") && strings.Contains(line, "C") {
			println("DEBUG: Found temp header:", line)
			currentBlock = "temp"
			collectingData = true
			isHeader = true
		} else if strings.Contains(line, "Schubkraft[N]") {
			println("DEBUG: Found force header:", line)
			currentBlock = "force"
			collectingData = true
			isHeader = true
		} else if strings.Contains(line, "Einblasdruck[bar]") {
			println("DEBUG: Found pressure header:", line)
			currentBlock = "pressure"
			collectingData = true
			isHeader = true
		} else if strings.Contains(line, "Geschwindigkeit[m/min]") {
			println("DEBUG: Found speed header:", line)
			currentBlock = "speed"
			collectingData = true
			isHeader = true
		} else if strings.Contains(line, "Zeit") && (strings.Contains(line, "Dauer") || strings.Contains(line, "hh:mm:ss")) {
			println("DEBUG: Found time header:", line)
			currentBlock = "time"
			collectingData = true
			isHeader = true
		}
		
		// If this is a header line, skip to next iteration 
		if isHeader {
			continue
		}
		
		// Collect values for current block only if we're actively collecting
		if collectingData && currentBlock != "" {
			println("DEBUG: Collecting for block:", currentBlock, "line:", line)
			switch currentBlock {
			case "length":
				lengthValues = append(lengthValues, line)
			case "temp":
				tempValues = append(tempValues, line)
			case "force":
				forceValues = append(forceValues, line)
			case "pressure":
				pressureValues = append(pressureValues, line)
			case "speed":
				speedValues = append(speedValues, line)
			case "time":
				timeValues = append(timeValues, line)
			}
		} else if strings.TrimSpace(line) != "" && !isHeader {
			println("DEBUG: Skipping line (not collecting):", line, "currentBlock:", currentBlock, "collectingData:", collectingData)
		}
	}
	
	// Build measurements from collected values - use the shortest array to avoid index errors
	var measurements []JettingMeasurement
	
	// Debug output
	println("DEBUG: parseVerticalBlocks collected data:")
	println("lengthValues count:", len(lengthValues))
	println("tempValues count:", len(tempValues)) 
	println("forceValues count:", len(forceValues))
	println("pressureValues count:", len(pressureValues))
	println("speedValues count:", len(speedValues))
	println("timeValues count:", len(timeValues))
	
	maxLen := len(lengthValues)
	if len(forceValues) < maxLen {
		maxLen = len(forceValues)
	}
	if len(pressureValues) < maxLen {
		maxLen = len(pressureValues)
	}
	if len(speedValues) < maxLen {
		maxLen = len(speedValues)
	}
	if len(timeValues) < maxLen {
		maxLen = len(timeValues)
	}
	
		for i := 0; i < maxLen; i++ {
		length, _ := strconv.ParseFloat(lengthValues[i], 64)
		temp, _ := strconv.ParseFloat(tempValues[i], 64)        // Lufttemperatur[°C] -> Temperature
		force, _ := strconv.ParseFloat(forceValues[i], 64)      // Schubkraft[N] -> Force 
		pressure, _ := strconv.ParseFloat(pressureValues[i], 64) // Einblasdruck[bar] -> Pressure
		speed, _ := strconv.ParseFloat(speedValues[i], 64)      // Geschwindigkeit[m/min] -> Speed
		time := timeValues[i]                                   // Zeit - Dauer[hh:mm:ss] -> Time
		
		measurements = append(measurements, JettingMeasurement{
			Length:      length,
			Temperature: temp,
			Force:       force,
			Pressure:    pressure,  
			Speed:       speed,     
			Time:        time,
		})
	}
	
	return measurements
}

// ParseJettingProtocol extracts comprehensive protocol data from Jetting PDF text
func ParseJettingProtocol(normalized string) *JettingProtocol {
	protocol := &JettingProtocol{
		ExportMetadata: JettingExportMetadata{
			ParsedAt:      time.Now(),
			ParserVersion: "1.0.0",
		},
	}
	
	lines := strings.Split(normalized, "\n")
	
	// Extract protocol info
	protocol.ProtocolInfo = extractJettingProtocolInfo(lines)
	
	// Extract equipment specifications (minimal for Jetting)
	protocol.Equipment = extractJettingEquipment(lines)
	
	// Extract measurements
	protocol.Measurements = extractJettingMeasurements(lines)
	
	return protocol
}

// extractJettingProtocolInfo parses header information from Jetting PDFs
func extractJettingProtocolInfo(lines []string) JettingProtocolInfo {
	info := JettingProtocolInfo{
		System:       "Jetting System",
		DocumentType: "Jetting Protokoll",
	}
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Company detection
		if strings.Contains(line, "Wolken-ASM") {
			info.Company = "Wolken-ASM GMBH"
		}
		
		if strings.Contains(line, "M.A.X. Bauservice") {
			info.ServiceProvider = "M.A.X. Bauservice"
		}
		
		// Date detection (from filename parsing, not PDF content)
		// This will be filled by filename parsing in main.go
	}
	
	return info
}

// extractJettingEquipment parses equipment info (minimal for Jetting)
func extractJettingEquipment(lines []string) JettingEquipment {
	// Jetting PDFs typically don't contain detailed equipment specifications
	// Return empty/null structure
	return JettingEquipment{
		BlowingDevice: JettingBlowingDevice{},
		Pipe:          JettingPipe{ColorCoding: []string{}},
		Cable:         JettingCable{},
		Compressor:    JettingCompressor{},
	}
}

// extractJettingMeasurements parses measurement data from Jetting PDFs
func extractJettingMeasurements(lines []string) JettingMeasurements {
	measurements := JettingMeasurements{
		MeterReadings: JettingMeterReadings{},
		Summary:       JettingSummary{},
		DataPoints:    []JettingDataPoint{},
	}
	
	// Parse measurement data points using existing function
	jettingMeasurements := ParseJettingTxt(strings.Join(lines, "\n"))
	for _, jm := range jettingMeasurements {
		measurements.DataPoints = append(measurements.DataPoints, JettingDataPoint{
			LengthM:      jm.Length,
			TemperatureC: jm.Temperature,
			ForceN:       jm.Force,
			PressureBar:  jm.Pressure,
			SpeedMMin:    jm.Speed,
			TimeDuration: jm.Time,
		})
	}
	
	return measurements
}
