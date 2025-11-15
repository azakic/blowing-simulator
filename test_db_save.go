package main

import (
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"blowing-simulator/internal/simulator"
)

func main() {
	// Connect to database inside docker container
	dbURL := "postgres://blowing:7fG2vQp9sXw3Lk8r@db:5432/blowing_simulator?sslmode=disable"
	db, err := sqlx.Connect("postgres", dbURL)
	if err != nil {
		log.Printf("Failed to connect to db container, trying localhost...")
		dbURL = "postgres://blowing:7fG2vQp9sXw3Lk8r@localhost:5432/blowing_simulator?sslmode=disable"
		db, err = sqlx.Connect("postgres", dbURL)
		if err != nil {
			log.Fatalf("Failed to connect to database: %v", err)
		}
	}
	defer db.Close()

	// Create a minimal test protocol
	testProtocol := &simulator.FremcoProtocol{
		ProtocolInfo: simulator.FremcoProtocolInfo{
			System:        "Test System",
			DocumentType:  "Test Document",
			Date:          "2025-11-15",
			StartTime:     "14:00",
			ProjectNumber: "TEST123",
			SectionNVT:    "Test Section",
			Company:       "Test Company",
			ServiceProvider: "Test Service",
			Operator:      "Test Operator",
			Remarks:       "Test Protocol",
		},
		Equipment: simulator.FremcoEquipment{
			BlowingDevice: simulator.FremcoBlowingDevice{
				Model:        "Test Model",
				ControllerSN: "TEST001",
			},
			Pipe: simulator.FremcoPipe{
				Manufacturer: "Test Manufacturer",
			},
			Cable: simulator.FremcoCable{
				Manufacturer: "Test Cable Manufacturer",
			},
			Compressor: simulator.FremcoCompressor{
				Model: "Test Compressor",
			},
		},
		Measurements: simulator.FremcoMeasurements{
			MeterReadings: simulator.FremcoMeterReadings{
				Start: 100,
				End:   200,
			},
			Summary: simulator.FremcoSummary{
				Distance:    100,
				BlowingTime: "00:10:00",
				Weather: simulator.FremcoWeather{
					Temperature: 20.0,
					Humidity:    50.0,
				},
				GPSLocation: simulator.FremcoGPS{
					Latitude:  52.0,
					Longitude: 9.0,
				},
			},
			DataPoints: []simulator.FremcoDataPoint{
				{
					LengthM:       0,
					SpeedMMin:     0,
					PressureBar:   26.5,
					TorquePercent: 1,
					Timestamp:     "2025-11-15 14:00:00",
				},
				{
					LengthM:       1,
					SpeedMMin:     25,
					PressureBar:   26.5,
					TorquePercent: 28,
					Timestamp:     "2025-11-15 14:00:10",
				},
			},
		},
		ExportMetadata: simulator.FremcoExportMetadata{
			ParsedAt:       time.Now(),
			ParserVersion:  "test-1.0.0",
			SourceFilename: "test_protocol.pdf",
		},
	}

	fmt.Println("Testing SaveFremcoProtocol with test data...")
	protocolID, err := SaveFremcoProtocol(db, testProtocol)
	if err != nil {
		log.Fatalf("SaveFremcoProtocol failed: %v", err)
	}

	fmt.Printf("Protocol saved with ID: %d\n", protocolID)

	// Check if measurements were saved
	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM protocol_measurements WHERE protocol_id = $1", protocolID)
	if err != nil {
		log.Fatalf("Failed to count measurements: %v", err)
	}

	fmt.Printf("Measurements saved: %d\n", count)
	
	if count == 0 {
		fmt.Println("ERROR: No measurements were saved!")
	} else {
		fmt.Println("SUCCESS: Measurements were saved correctly")
	}
}