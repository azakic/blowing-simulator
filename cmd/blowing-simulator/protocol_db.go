package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"blowing-simulator/internal/simulator"
)

// SaveFremcoProtocol saves a complete Fremco protocol to the database
func SaveFremcoProtocol(db *sqlx.DB, protocol *simulator.FremcoProtocol) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Insert main protocol record
	var protocolID int
	err = tx.QueryRow(`
		INSERT INTO protocols (
			protocol_type, system_name, document_type, protocol_date, start_time,
			project_number, section_nvt, company, service_provider, operator,
			remarks, source_filename, parser_version, address
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`,
		"fremco",
		protocol.ProtocolInfo.System,
		protocol.ProtocolInfo.DocumentType,
		parseDate(protocol.ProtocolInfo.Date),
		parseTime(protocol.ProtocolInfo.StartTime),
		protocol.ProtocolInfo.ProjectNumber,
		protocol.ProtocolInfo.SectionNVT,
		protocol.ProtocolInfo.Company,
		protocol.ProtocolInfo.ServiceProvider,
		protocol.ProtocolInfo.Operator,
		protocol.ProtocolInfo.Remarks,
		protocol.ExportMetadata.SourceFilename,
		protocol.ExportMetadata.ParserVersion,
		extractAddressFromSectionNVT(protocol.ProtocolInfo.SectionNVT),
	).Scan(&protocolID)

	if err != nil {
		return 0, fmt.Errorf("failed to insert protocol: %v", err)
	}

	// Insert equipment specifications
	_, err = tx.Exec(`
		INSERT INTO protocol_equipment (
			protocol_id, device_model, controller_sn, lubricator, crash_test_performed,
			crash_test_speed, crash_test_moment, pipe_manufacturer, pipe_bundle,
			pipe_type, pipe_color_coding, pipe_inner_wall, pipe_temperature,
			cable_manufacturer, cable_designation, cable_fiber_count, cable_diameter,
			cable_temperature, cable_lubricant, cable_blowing_cap,
			compressor_model, compressor_oil_separator, compressor_after_cooler
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)`,
		protocolID,
		protocol.Equipment.BlowingDevice.Model,
		protocol.Equipment.BlowingDevice.ControllerSN,
		protocol.Equipment.BlowingDevice.Lubricator,
		protocol.Equipment.BlowingDevice.CrashTestPerformed,
		protocol.Equipment.BlowingDevice.CrashTestSpeed,
		protocol.Equipment.BlowingDevice.CrashTestMoment,
		protocol.Equipment.Pipe.Manufacturer,
		protocol.Equipment.Pipe.PipeBundle,
		protocol.Equipment.Pipe.PipeType,
		pq.Array(protocol.Equipment.Pipe.ColorCoding),
		protocol.Equipment.Pipe.InnerWall,
		protocol.Equipment.Pipe.Temperature,
		protocol.Equipment.Cable.Manufacturer,
		protocol.Equipment.Cable.Designation,
		protocol.Equipment.Cable.FiberCount,
		protocol.Equipment.Cable.Diameter,
		protocol.Equipment.Cable.Temperature,
		protocol.Equipment.Cable.Lubricant,
		protocol.Equipment.Cable.BlowingCap,
		protocol.Equipment.Compressor.Model,
		protocol.Equipment.Compressor.OilSeparator,
		protocol.Equipment.Compressor.AfterCooler,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to insert equipment: %v", err)
	}

	// Insert protocol summary
	_, err = tx.Exec(`
		INSERT INTO protocol_summary (
			protocol_id, meter_start, meter_end, total_distance, blowing_time,
			weather_temperature, weather_humidity, gps_latitude, gps_longitude
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		protocolID,
		protocol.Measurements.MeterReadings.Start,
		protocol.Measurements.MeterReadings.End,
		protocol.Measurements.Summary.Distance,
		parseDuration(protocol.Measurements.Summary.BlowingTime),
		protocol.Measurements.Summary.Weather.Temperature,
		protocol.Measurements.Summary.Weather.Humidity,
		protocol.Measurements.Summary.GPSLocation.Latitude,
		protocol.Measurements.Summary.GPSLocation.Longitude,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to insert summary: %v", err)
	}

	// Insert measurement data points
	log.Printf("SaveFremcoProtocol: Starting to insert %d measurements for protocol ID %d", len(protocol.Measurements.DataPoints), protocolID)
	for i, dataPoint := range protocol.Measurements.DataPoints {
		parsedTimestamp := parseTimestamp(dataPoint.Timestamp)
		log.Printf("SaveFremcoProtocol: Inserting measurement %d - Length: %f, Speed: %f, Timestamp: %s -> Parsed: %v", 
			i, dataPoint.LengthM, dataPoint.SpeedMMin, dataPoint.Timestamp, parsedTimestamp)
		
		_, err = tx.Exec(`
			INSERT INTO protocol_measurements (
				protocol_id, length_m, speed_m_min, pressure_bar, torque_percent,
				timestamp_value, sequence_number
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			protocolID,
			dataPoint.LengthM,
			dataPoint.SpeedMMin,
			dataPoint.PressureBar,
			dataPoint.TorquePercent,
			parsedTimestamp,
			i+1,
		)

		if err != nil {
			log.Printf("SaveFremcoProtocol: Failed to insert measurement %d: %v", i, err)
			return 0, fmt.Errorf("failed to insert measurement %d: %v", i, err)
		}
		
		if i < 3 {  // Log first few measurements
			log.Printf("SaveFremcoProtocol: Successfully inserted measurement %d", i)
		}
	}
	log.Printf("SaveFremcoProtocol: Completed inserting all %d measurements", len(protocol.Measurements.DataPoints))

	log.Printf("SaveFremcoProtocol: Attempting to commit transaction for protocol ID %d", protocolID)
	if err = tx.Commit(); err != nil {
		log.Printf("SaveFremcoProtocol: Transaction commit failed: %v", err)
		return 0, fmt.Errorf("failed to commit transaction: %v", err)
	}

	log.Printf("SaveFremcoProtocol: Transaction committed successfully for protocol ID %d", protocolID)
	return protocolID, nil
}

// SaveJettingProtocol saves a complete Jetting protocol to the database
func SaveJettingProtocol(db *sqlx.DB, protocol *simulator.JettingProtocol) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %v", err)
	}
	defer tx.Rollback()

	// Insert main protocol record
	var protocolID int
	err = tx.QueryRow(`
		INSERT INTO protocols (
			protocol_type, system_name, document_type, protocol_date, start_time,
			project_number, section_nvt, company, service_provider, operator,
			remarks, source_filename, parser_version, address
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`,
		"jetting",
		protocol.ProtocolInfo.System,
		protocol.ProtocolInfo.DocumentType,
		parseDate(protocol.ProtocolInfo.Date),
		parseTime(protocol.ProtocolInfo.StartTime),
		protocol.ProtocolInfo.ProjectNumber,
		protocol.ProtocolInfo.SectionNVT,
		protocol.ProtocolInfo.Company,
		protocol.ProtocolInfo.ServiceProvider,
		protocol.ProtocolInfo.Operator,
		protocol.ProtocolInfo.Remarks,
		protocol.ExportMetadata.SourceFilename,
		protocol.ExportMetadata.ParserVersion,
		extractAddressFromSectionNVT(protocol.ProtocolInfo.SectionNVT),
	).Scan(&protocolID)

	if err != nil {
		return 0, fmt.Errorf("failed to insert protocol: %v", err)
	}

	// Insert minimal equipment specifications for Jetting
	_, err = tx.Exec(`
		INSERT INTO protocol_equipment (protocol_id, pipe_color_coding) VALUES ($1, $2)`,
		protocolID,
		pq.Array(protocol.Equipment.Pipe.ColorCoding),
	)

	if err != nil {
		return 0, fmt.Errorf("failed to insert equipment: %v", err)
	}

	// Insert minimal summary for Jetting
	_, err = tx.Exec(`
		INSERT INTO protocol_summary (protocol_id) VALUES ($1)`,
		protocolID,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to insert summary: %v", err)
	}

	// Insert measurement data points
	for i, dataPoint := range protocol.Measurements.DataPoints {
		_, err = tx.Exec(`
			INSERT INTO protocol_measurements (
				protocol_id, length_m, temperature_c, force_n, pressure_bar,
				speed_m_min, time_duration, sequence_number
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			protocolID,
			dataPoint.LengthM,
			dataPoint.TemperatureC,
			dataPoint.ForceN,
			dataPoint.PressureBar,
			dataPoint.SpeedMMin,
			parseDuration(dataPoint.TimeDuration),
			i+1,
		)

		if err != nil {
			return 0, fmt.Errorf("failed to insert measurement %d: %v", i, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %v", err)
	}

	return protocolID, nil
}

// LoadProtocol loads a protocol by ID (works for both Fremco and Jetting)
func LoadProtocol(db *sqlx.DB, protocolID int) (interface{}, error) {
	var protocolType string
	err := db.QueryRow("SELECT protocol_type FROM protocols WHERE id = $1", protocolID).Scan(&protocolType)
	if err != nil {
		return nil, fmt.Errorf("failed to get protocol type: %v", err)
	}

	if protocolType == "fremco" {
		return LoadFremcoProtocol(db, protocolID)
	} else if protocolType == "jetting" {
		return LoadJettingProtocol(db, protocolID)
	}

	return nil, fmt.Errorf("unknown protocol type: %s", protocolType)
}

// LoadFremcoProtocol loads a complete Fremco protocol from the database
func LoadFremcoProtocol(db *sqlx.DB, protocolID int) (*simulator.FremcoProtocol, error) {
	protocol := &simulator.FremcoProtocol{}

	// Load protocol info
	err := db.QueryRow(`
		SELECT system_name, document_type, protocol_date, start_time, project_number,
		       section_nvt, company, service_provider, operator, remarks,
		       source_filename, parser_version, parsed_at
		FROM protocols WHERE id = $1`, protocolID).Scan(
		&protocol.ProtocolInfo.System,
		&protocol.ProtocolInfo.DocumentType,
		&protocol.ProtocolInfo.Date,
		&protocol.ProtocolInfo.StartTime,
		&protocol.ProtocolInfo.ProjectNumber,
		&protocol.ProtocolInfo.SectionNVT,
		&protocol.ProtocolInfo.Company,
		&protocol.ProtocolInfo.ServiceProvider,
		&protocol.ProtocolInfo.Operator,
		&protocol.ProtocolInfo.Remarks,
		&protocol.ExportMetadata.SourceFilename,
		&protocol.ExportMetadata.ParserVersion,
		&protocol.ExportMetadata.ParsedAt,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to load protocol info: %v", err)
	}

	// Load equipment (implementation continues...)
	// Load summary (implementation continues...)
	// Load measurements (implementation continues...)

	return protocol, nil
}

// LoadJettingProtocol loads a complete Jetting protocol from the database
func LoadJettingProtocol(db *sqlx.DB, protocolID int) (*simulator.JettingProtocol, error) {
	// Similar implementation to LoadFremcoProtocol but for Jetting type
	protocol := &simulator.JettingProtocol{}
	// Implementation continues...
	return protocol, nil
}

// Helper functions for parsing dates, times, and durations
func parseDate(dateStr string) *time.Time {
	if dateStr == "" {
		return nil
	}
	
	// Try multiple date formats
	formats := []string{"2006-01-02", "02.01.2006", "01/02/2006"}
	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return &t
		}
	}
	return nil
}

func parseTime(timeStr string) *time.Time {
	if timeStr == "" {
		return nil
	}
	
	// Try multiple time formats
	formats := []string{"15:04", "15.04", "15:04:05"}
	for _, format := range formats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return &t
		}
	}
	return nil
}

func parseDuration(durationStr string) *time.Duration {
	if durationStr == "" {
		return nil
	}
	
	// Parse duration in format "hh:mm:ss"
	if d, err := time.ParseDuration(durationStr); err == nil {
		return &d
	}
	return nil
}

func parseTimestamp(timestampStr string) *time.Time {
	if timestampStr == "" {
		log.Printf("parseTimestamp: Empty timestamp string")
		return nil
	}
	
	log.Printf("parseTimestamp: Parsing timestamp: '%s'", timestampStr)
	
	// Try multiple timestamp formats
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",      // Added this format for timestamps without seconds
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",      // Added this format too
		"02.01.2006 15:04:05",
		"02.01.2006 15:04",      // And this one
	}
	
	for i, format := range formats {
		if t, err := time.Parse(format, timestampStr); err == nil {
			log.Printf("parseTimestamp: Successfully parsed '%s' using format %d (%s) -> %v", timestampStr, i, format, t)
			return &t
		} else {
			log.Printf("parseTimestamp: Failed format %d (%s): %v", i, format, err)
		}
	}
	
	log.Printf("parseTimestamp: All formats failed for '%s'", timestampStr)
	return nil
}

// Helper to extract address from SectionNVT string
func extractAddressFromSectionNVT(sectionNVT string) string {
    // If SectionNVT is in format "address / NVT", extract address part
    parts := strings.Split(sectionNVT, "/")
    if len(parts) > 0 {
        return strings.TrimSpace(parts[0])
    }
    return sectionNVT
}