package simulator

import (
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SimpleMeasurement represents a single measurement in the uniform format (for Fremco).
type SimpleMeasurement struct {
	Length   float64 `json:"length"`
	Speed    float64 `json:"speed"`
	Pressure float64 `json:"pressure"`
	Torque   float64 `json:"torque"`
	Time     string  `json:"time"`
}

// JettingMeasurement represents a Jetting measurement with German field names.
type JettingMeasurement struct {
	Length      float64 `json:"länge_m"`
	Temperature float64 `json:"lufttemperatur_c"`
	Force       float64 `json:"schubkraft_n"`
	Pressure    float64 `json:"einblasdruck_bar"`
	Speed       float64 `json:"geschwindigkeit_m_min"`
	Time        string  `json:"zeit_dauer_hh_mm_ss"`
}

// ExtractFremcoMetadata parses normalized Fremco text and returns a map of metadata fields.
func ExtractFremcoMetadata(normalized string) map[string]string {
	meta := make(map[string]string)
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Bauvorhaben Nr.") {
			// Project number
			if i+1 < len(lines) {
				meta["Project"] = strings.TrimSpace(lines[i+1])
			}
		}
		if strings.HasPrefix(line, "Datum, Startzeit:") {
			// Date and time
			if i+1 < len(lines) {
				dt := strings.TrimSpace(lines[i+1])
				parts := strings.Split(dt, " ")
				if len(parts) >= 2 {
					meta["Date"] = parts[0]
					meta["Time"] = parts[1]
				} else {
					meta["Date"] = dt
				}
			}
		}
		if strings.HasPrefix(line, "Streckenabschnitt / NVt") {
			// Address and NVT
			if i+1 < len(lines) {
				addrNvt := strings.TrimSpace(lines[i+1])
				addrParts := strings.Split(addrNvt, "/")
				if len(addrParts) >= 2 {
					meta["Address"] = strings.TrimSpace(addrParts[0])
					meta["NVT"] = strings.TrimSpace(addrParts[1])
				} else {
					meta["Address"] = addrNvt
				}
			}
		}
		if strings.HasPrefix(line, "Firma") {
			if i+1 < len(lines) {
				meta["Company"] = strings.TrimSpace(lines[i+1])
			}
		}
		if strings.HasPrefix(line, "Einblasgerät:") {
			meta["Device"] = strings.TrimPrefix(line, "Einblasgerät:")
			meta["Device"] = strings.TrimSpace(meta["Device"])
		}
		if strings.HasPrefix(line, "Bezeichnung:") {
			meta["Cable"] = strings.TrimPrefix(line, "Bezeichnung:")
			meta["Cable"] = strings.TrimSpace(meta["Cable"])
		}
		if strings.HasPrefix(line, "Meterzahlen:") {
			// Meter range
			if i+1 < len(lines) {
				meta["MeterRange"] = strings.TrimSpace(lines[i+1])
			}
		}
		if strings.HasPrefix(line, "Zusammenfassung") {
			// Strecke, Einblaszeit, Wetter, Ort (GPS)
			if i+1 < len(lines) {
				sum := lines[i+1]
				// Example: Strecke: 370 Einblaszeit: 00:38:25 Wetter: 19.3°C, 69.5%RH Ort (GPS): 52.48653, 9.85465
				if strings.Contains(sum, "Strecke:") {
					parts := strings.Split(sum, "Strecke:")
					if len(parts) > 1 {
						rest := parts[1]
						fields := strings.Fields(rest)
						if len(fields) > 0 {
							meta["Strecke"] = fields[0]
						}
					}
				}
				if strings.Contains(sum, "Einblaszeit:") {
					parts := strings.Split(sum, "Einblaszeit:")
					if len(parts) > 1 {
						meta["Einblaszeit"] = strings.Fields(parts[1])[0]
					}
				}
				if strings.Contains(sum, "Wetter:") {
					parts := strings.Split(sum, "Wetter:")
					if len(parts) > 1 {
						meta["Wetter"] = strings.Split(parts[1], "Ort")[0]
						meta["Wetter"] = strings.TrimSpace(meta["Wetter"])
					}
				}
				if strings.Contains(sum, "Ort (GPS):") {
					parts := strings.Split(sum, "Ort (GPS):")
					if len(parts) > 1 {
						meta["GPS"] = strings.TrimSpace(parts[1])
					}
				}
			}
		}
	}
	return meta
}

// ParseFremcoSimple parses normalized Fremco TXT and returns a slice of SimpleMeasurement.
// It expects the table header to start with "Streckenlänge" and each row to have:
// Length [m], Speed [m/min], Pressure [bar], Torque [%], DateTime [hh:mm:ss]
func ParseFremcoSimple(normalized string) []SimpleMeasurement {
	lines := strings.Split(normalized, "\n")
	var measurements []SimpleMeasurement
	inTable := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Detect start of table (accept both German and English headers)
		if strings.HasPrefix(line, "Länge") || strings.HasPrefix(line, "Streckenlänge") {
			inTable = true
			continue
		}
		// Skip headers inside the table
		if strings.HasPrefix(line, "Lufttemperatur") ||
			strings.HasPrefix(line, "Schubkraft") ||
			strings.HasPrefix(line, "Einblasdruck") ||
			strings.HasPrefix(line, "Geschwindigkeit") ||
			strings.HasPrefix(line, "Zeit - Dauer") ||
			strings.HasPrefix(line, "[m]") || strings.HasPrefix(line, "[°C]") {
			continue
		}
		if inTable {
			fields := strings.Fields(line)
			// Accept rows with at least 5 columns: length, speed, pressure, torque, time
			if len(fields) >= 5 {
				length, _ := strconv.ParseFloat(fields[0], 64)
				speed, _ := strconv.ParseFloat(fields[1], 64)
				pressure, _ := strconv.ParseFloat(fields[2], 64)
				torque, _ := strconv.ParseFloat(fields[3], 64)
				time := fields[4]
				// If time field is split (date + time), join them
				if len(fields) > 5 && isDateTime(fields[4]+" "+fields[5]) {
					time = fields[4] + " " + fields[5]
				}
				measurements = append(measurements, SimpleMeasurement{
					Length:   length,
					Speed:    speed,
					Pressure: pressure,
					Torque:   torque,
					Time:     time,
				})
			}
		}
	}
	return measurements
}

// Helper functions
func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func isDateTime(s string) bool {
	// Accepts formats like "2024-07-26 14:41:07"
	return len(s) >= 16 && s[4] == '-' && s[7] == '-' && (s[10] == ' ' || s[10] == 'T')
}

// ParseFremcoProtocol extracts comprehensive protocol data from Fremco PDF text
func ParseFremcoProtocol(normalized string) *FremcoProtocol {
	protocol := &FremcoProtocol{
		ExportMetadata: FremcoExportMetadata{
			ParsedAt:      time.Now(),
			ParserVersion: "1.0.0",
		},
	}
	
	lines := strings.Split(normalized, "\n")
	
	// Extract protocol info
	protocol.ProtocolInfo = extractFremcoProtocolInfo(lines)
	
	// Extract equipment specifications
	protocol.Equipment = extractFremcoEquipment(lines)
	
	// Extract measurements and summary
	protocol.Measurements = extractFremcoMeasurements(lines)
	
	return protocol
}

// extractFremcoProtocolInfo parses header information
func extractFremcoProtocolInfo(lines []string) FremcoProtocolInfo {
	info := FremcoProtocolInfo{
		System:       "SpeedNet-System",
		DocumentType: "Einblas - Protokoll",
	}
	
	// Debug: log first 20 lines to understand structure
	log.Printf("extractFremcoProtocolInfo: Parsing %d lines of text", len(lines))
	for i := 0; i < len(lines) && i < 20; i++ {
		log.Printf("Line %d: '%s'", i, strings.TrimSpace(lines[i]))
	}
	
	for i, line := range lines {
		line = strings.TrimSpace(line)
		
		// Project number - extract from same line after "Bauvorhaben Nr."
		if strings.Contains(line, "Bauvorhaben Nr.") {
			// Extract project number from same line
			projectMatch := regexp.MustCompile(`Bauvorhaben Nr\.\s*(.+)`).FindStringSubmatch(line)
			if len(projectMatch) > 1 {
				info.ProjectNumber = strings.TrimSpace(projectMatch[1])
				log.Printf("extractFremcoProtocolInfo: Found Project Number - Line %d: '%s' -> Extracted: '%s'", i, line, info.ProjectNumber)
				
				// Date and time should be on the next line after project number
				if i+1 < len(lines) {
					dateTime := strings.TrimSpace(lines[i+1])
					parts := strings.Fields(dateTime)
					log.Printf("extractFremcoProtocolInfo: Found Date/Time - Line %d: '%s' -> Parts: %v", i+1, dateTime, parts)
					if len(parts) >= 2 {
						info.Date = parts[0]
						info.StartTime = parts[1]
						log.Printf("extractFremcoProtocolInfo: Parsed Date: '%s', Time: '%s'", info.Date, info.StartTime)
					}
				}
			}
		}
		
		// Section/NVT
		if strings.Contains(line, "Streckenabschnitt") && strings.Contains(line, "NVt") && i+1 < len(lines) {
			info.SectionNVT = strings.TrimSpace(lines[i+1])
		}
		
		// Company - extract from same line after "Firma"
		if strings.Contains(line, "Firma") {
			// Extract company from same line
			companyMatch := regexp.MustCompile(`Firma\s+(.+)`).FindStringSubmatch(line)
			if len(companyMatch) > 1 {
				info.Company = strings.TrimSpace(companyMatch[1])
				log.Printf("extractFremcoProtocolInfo: Found Company - Line %d: '%s' -> Extracted: '%s'", i, line, info.Company)
			}
		}
		
		// Service provider
		if strings.Contains(line, "M.A.X. Bauservice") {
			info.ServiceProvider = "M.A.X. Bauservice"
		}
		
		// Operator
		if strings.Contains(line, "Einbläser:") {
			operatorMatch := regexp.MustCompile(`Einbläser:\s*(\w+)`).FindStringSubmatch(line)
			if len(operatorMatch) > 1 {
				info.Operator = operatorMatch[1]
			}
		}
		
		// Remarks
		if strings.Contains(line, "Bemerkungen") && i+1 < len(lines) {
			info.Remarks = strings.TrimSpace(lines[i+1])
		}
	}
	
	return info
}

// extractFremcoEquipment parses equipment specifications
func extractFremcoEquipment(lines []string) FremcoEquipment {
	equipment := FremcoEquipment{}
	
	for i, line := range lines {
		line = strings.TrimSpace(line)
		
		// Blowing Device
		if strings.Contains(line, "Einblasgerät:") {
			deviceMatch := regexp.MustCompile(`Einblasgerät:\s*(.+)`).FindStringSubmatch(line)
			if len(deviceMatch) > 1 {
				equipment.BlowingDevice.Model = strings.TrimSpace(deviceMatch[1])
			}
		}
		
		if strings.Contains(line, "Controller S/N:") {
			snMatch := regexp.MustCompile(`Controller S/N:\s*(.+)`).FindStringSubmatch(line)
			if len(snMatch) > 1 {
				equipment.BlowingDevice.ControllerSN = strings.TrimSpace(snMatch[1])
			}
		}
		
		if strings.Contains(line, "+ Lubricator") {
			equipment.BlowingDevice.Lubricator = strings.Contains(line, "ja")
		}
		
		if strings.Contains(line, "+ Crashtest Durchgeführt") {
			equipment.BlowingDevice.CrashTestPerformed = strings.Contains(line, "ja")
		}
		
		// Pipe specifications
		if strings.Contains(line, "Hersteller:") && strings.Contains(line, "Gabocom") {
			equipment.Pipe.Manufacturer = "Gabocom"
		}
		
		if strings.Contains(line, "Rohrverband:") {
			bundleMatch := regexp.MustCompile(`Rohrverband:\s*(.+?)(?:\s|$)`).FindStringSubmatch(line)
			if len(bundleMatch) > 1 {
				equipment.Pipe.PipeBundle = strings.TrimSpace(bundleMatch[1])
			}
		}
		
		if strings.Contains(line, "Rohr:") {
			pipeMatch := regexp.MustCompile(`Rohr:\s*(.+?)(?:\s|$)`).FindStringSubmatch(line)
			if len(pipeMatch) > 1 {
				equipment.Pipe.PipeType = strings.TrimSpace(pipeMatch[1])
			}
		}
		
		if strings.Contains(line, "Farbe-Kennung:") {
			colorMatch := regexp.MustCompile(`Farbe-Kennung:\s*(.+)`).FindStringSubmatch(line)
			if len(colorMatch) > 1 {
				colors := strings.Fields(colorMatch[1])
				equipment.Pipe.ColorCoding = colors
			}
		}
		
		if strings.Contains(line, "Rohrinnenwand:") {
			wallMatch := regexp.MustCompile(`Rohrinnenwand:\s*(.+)`).FindStringSubmatch(line)
			if len(wallMatch) > 1 {
				equipment.Pipe.InnerWall = strings.TrimSpace(wallMatch[1])
			}
		}
		
		// Cable specifications
		if strings.Contains(line, "Hersteller:") && strings.Contains(line, "Prysmian") {
			equipment.Cable.Manufacturer = "Prysmian"
		}
		
		if strings.Contains(line, "Bezeichnung:") {
			designMatch := regexp.MustCompile(`Bezeichnung:\s*(.+)`).FindStringSubmatch(line)
			if len(designMatch) > 1 {
				equipment.Cable.Designation = strings.TrimSpace(designMatch[1])
			}
		}
		
		if strings.Contains(line, "Faserzahl:") {
			fiberMatch := regexp.MustCompile(`Faserzahl:\s*(\d+)`).FindStringSubmatch(line)
			if len(fiberMatch) > 1 {
				if count, err := strconv.Atoi(fiberMatch[1]); err == nil {
					equipment.Cable.FiberCount = count
				}
			}
		}
		
		if strings.Contains(line, "Kabel-Durchmesser:") && i+1 < len(lines) {
			diameterStr := strings.TrimSpace(lines[i+1])
			if diameter, err := strconv.ParseFloat(diameterStr, 64); err == nil {
				equipment.Cable.Diameter = diameter
			}
		}
		
		if strings.Contains(line, "Gleitmittel:") {
			lubricantMatch := regexp.MustCompile(`Gleitmittel:\s*(.+)`).FindStringSubmatch(line)
			if len(lubricantMatch) > 1 {
				equipment.Cable.Lubricant = strings.TrimSpace(lubricantMatch[1])
			}
		}
		
		if strings.Contains(line, "Kabel-Temperatur:") {
			tempMatch := regexp.MustCompile(`Kabel-Temperatur:\s*(\d+(?:\.\d+)?)°C`).FindStringSubmatch(line)
			if len(tempMatch) > 1 {
				if temp, err := strconv.ParseFloat(tempMatch[1], 64); err == nil {
					equipment.Cable.Temperature = &temp
				}
			}
		}
		
		if strings.Contains(line, "Kabel-Einblaskappe:") {
			equipment.Cable.BlowingCap = strings.Contains(line, "ja")
		}
		
		// Compressor specifications
		if strings.Contains(line, "Kompressor:") {
			compressorMatch := regexp.MustCompile(`Kompressor:\s*(.+)`).FindStringSubmatch(line)
			if len(compressorMatch) > 1 {
				equipment.Compressor.Model = strings.TrimSpace(compressorMatch[1])
			}
		}
		
		if strings.Contains(line, "+ Ölabscheider") {
			equipment.Compressor.OilSeparator = strings.Contains(line, "ja")
		}
		
		if strings.Contains(line, "+ Nachkühler") {
			equipment.Compressor.AfterCooler = strings.Contains(line, "ja")
		}
	}
	
	return equipment
}

// extractFremcoMeasurements parses measurements and summary data
func extractFremcoMeasurements(lines []string) FremcoMeasurements {
	measurements := FremcoMeasurements{
		DataPoints: []FremcoDataPoint{},
	}
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Meter readings
		if strings.Contains(line, "Start:") && strings.Contains(line, "Ende:") {
			meterMatch := regexp.MustCompile(`Start:\s*(\d+)\s*\|\s*Ende:\s*(\d+)`).FindStringSubmatch(line)
			if len(meterMatch) > 2 {
				if start, err := strconv.Atoi(meterMatch[1]); err == nil {
					measurements.MeterReadings.Start = start
				}
				if end, err := strconv.Atoi(meterMatch[2]); err == nil {
					measurements.MeterReadings.End = end
				}
			}
		}
		
		// Summary data
		if strings.Contains(line, "Strecke:") && strings.Contains(line, "Einblaszeit:") {
			summaryMatch := regexp.MustCompile(`Strecke:\s*(\d+)\s+Einblaszeit:\s*([\d:]+)`).FindStringSubmatch(line)
			if len(summaryMatch) > 2 {
				if distance, err := strconv.Atoi(summaryMatch[1]); err == nil {
					measurements.Summary.Distance = distance
				}
				measurements.Summary.BlowingTime = summaryMatch[2]
			}
		}
		
		// Weather data
		if strings.Contains(line, "Wetter:") {
			weatherMatch := regexp.MustCompile(`Wetter:\s*([\d.]+)°C,\s*([\d.]+)%RH`).FindStringSubmatch(line)
			if len(weatherMatch) > 2 {
				if temp, err := strconv.ParseFloat(weatherMatch[1], 64); err == nil {
					measurements.Summary.Weather.Temperature = temp
				}
				if humidity, err := strconv.ParseFloat(weatherMatch[2], 64); err == nil {
					measurements.Summary.Weather.Humidity = humidity
				}
			}
		}
		
		// GPS data
		if strings.Contains(line, "Ort (GPS):") {
			gpsMatch := regexp.MustCompile(`Ort \(GPS\):\s*([\d.]+),\s*([\d.]+)`).FindStringSubmatch(line)
			if len(gpsMatch) > 2 {
				if lat, err := strconv.ParseFloat(gpsMatch[1], 64); err == nil {
					measurements.Summary.GPSLocation.Latitude = lat
				}
				if lon, err := strconv.ParseFloat(gpsMatch[2], 64); err == nil {
					measurements.Summary.GPSLocation.Longitude = lon
				}
			}
		}
	}
	
	// Parse measurement data points using existing function
	simpleMeasurements := ParseFremcoSimple(strings.Join(lines, "\n"))
	for _, sm := range simpleMeasurements {
		measurements.DataPoints = append(measurements.DataPoints, FremcoDataPoint{
			LengthM:       sm.Length,
			SpeedMMin:     sm.Speed,
			PressureBar:   sm.Pressure,
			TorquePercent: sm.Torque,
			Timestamp:     sm.Time,
		})
	}
	
	return measurements
}
