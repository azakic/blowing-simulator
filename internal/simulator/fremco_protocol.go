package simulator

import "time"

// FremcoProtocol represents the complete Fremco Einblas-Protokoll data structure
type FremcoProtocol struct {
	ProtocolInfo   FremcoProtocolInfo   `json:"protocol_info"`
	Equipment      FremcoEquipment      `json:"equipment"`
	Measurements   FremcoMeasurements   `json:"measurements"`
	ExportMetadata FremcoExportMetadata `json:"export_metadata"`
}

// FremcoProtocolInfo contains header information from the protocol
type FremcoProtocolInfo struct {
	System          string `json:"system"`           // SpeedNet-System
	DocumentType    string `json:"document_type"`    // Einblas - Protokoll
	Date            string `json:"date"`             // 2025-10-22
	StartTime       string `json:"start_time"`       // 13:52
	ProjectNumber   string `json:"project_number"`   // SM209214964
	SectionNVT      string `json:"section_nvt"`      // Haflinger Weg 5 / NVT1V3400
	Company         string `json:"company"`          // Wolken-ASM GmbH
	ServiceProvider string `json:"service_provider"` // M.A.X. Bauservice
	Operator        string `json:"operator"`         // Marko
	Remarks         string `json:"remarks"`
}

// FremcoEquipment contains all equipment specifications
type FremcoEquipment struct {
	BlowingDevice FremcoBlowingDevice `json:"blowing_device"`
	Pipe          FremcoPipe          `json:"pipe"`
	Cable         FremcoCable         `json:"cable"`
	Compressor    FremcoCompressor    `json:"compressor"`
}

// FremcoBlowingDevice represents the Einblasgerät specifications
type FremcoBlowingDevice struct {
	Model                string  `json:"model"`                  // Fremco MicroFlow LOG
	ControllerSN         string  `json:"controller_sn"`          // 9328.5155
	Lubricator           bool    `json:"lubricator"`             // + Lubricator [ ja ]
	CrashTestPerformed   bool    `json:"crash_test_performed"`   // + Crashtest Durchgeführt [ nein ]
	CrashTestSpeed       *string `json:"crash_test_speed"`       // + Crashtest Geschwindigkeit [ ]
	CrashTestMoment      *string `json:"crash_test_moment"`      // + Crashtest Moment [ ]
}

// FremcoPipe represents pipe specifications
type FremcoPipe struct {
	Manufacturer string   `json:"manufacturer"`  // Gabocom
	PipeBundle   string   `json:"pipe_bundle"`   // SNRVe 22x7x1,5
	PipeType     string   `json:"pipe_type"`     // SNR 7x1,5
	ColorCoding  []string `json:"color_coding"`  // ["Braun", "Schwarz"]
	InnerWall    string   `json:"inner_wall"`    // Gerieft
	Temperature  *float64 `json:"temperature"`   // °C (nullable)
}

// FremcoCable represents cable specifications
type FremcoCable struct {
	Manufacturer string   `json:"manufacturer"`  // Prysmian
	Designation  string   `json:"designation"`   // A-D 2Y 1x6
	FiberCount   int      `json:"fiber_count"`   // 6
	Diameter     float64  `json:"diameter"`      // 2.5
	Temperature  *float64 `json:"temperature"`   // 14°C (nullable)
	Lubricant    string   `json:"lubricant"`     // Prelube 5000
	BlowingCap   bool     `json:"blowing_cap"`   // nein
}

// FremcoCompressor represents compressor specifications
type FremcoCompressor struct {
	Model         string `json:"model"`          // Kaiser m17a
	OilSeparator  bool   `json:"oil_separator"`  // + Ölabscheider [ nein ]
	AfterCooler   bool   `json:"after_cooler"`   // + Nachkühler [ ja ]
}

// FremcoMeasurements contains all measurement data
type FremcoMeasurements struct {
	MeterReadings FremcoMeterReadings `json:"meter_readings"`
	Summary       FremcoSummary       `json:"summary"`
	DataPoints    []FremcoDataPoint   `json:"data_points"`
}

// FremcoMeterReadings represents meter readings
type FremcoMeterReadings struct {
	Start int `json:"start"` // 3365
	End   int `json:"end"`   // 3209
}

// FremcoSummary contains summary information
type FremcoSummary struct {
	Distance    int              `json:"distance"`      // 150
	BlowingTime string           `json:"blowing_time"`  // 00:05:48
	Weather     FremcoWeather    `json:"weather"`
	GPSLocation FremcoGPSLocation `json:"gps_location"`
}

// FremcoWeather represents weather conditions
type FremcoWeather struct {
	Temperature float64 `json:"temperature"` // 19.3°C
	Humidity    float64 `json:"humidity"`    // 61.6%RH
}

// FremcoGPSLocation represents GPS coordinates
type FremcoGPSLocation struct {
	Latitude  float64 `json:"latitude"`  // 52.48654
	Longitude float64 `json:"longitude"` // 9.85468
}

// FremcoDataPoint represents individual measurement point
type FremcoDataPoint struct {
	LengthM       float64 `json:"length_m"`        // Streckenlänge [m]
	SpeedMMin     float64 `json:"speed_m_min"`     // Geschwindigkeit [m/min]
	PressureBar   float64 `json:"pressure_bar"`    // Rohr-Druck [bar]
	TorquePercent float64 `json:"torque_percent"`  // Drehmoment [%]
	Timestamp     string  `json:"timestamp"`       // Uhrzeit [hh:mm:ss] or full timestamp
}

// FremcoExportMetadata contains export/parsing metadata
type FremcoExportMetadata struct {
	ParsedAt       time.Time `json:"parsed_at"`
	ParserVersion  string    `json:"parser_version"`
	SourceFilename string    `json:"source_filename"`
}