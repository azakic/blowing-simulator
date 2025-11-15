package simulator

import "time"

// JettingProtocol represents the complete Jetting protocol data structure
type JettingProtocol struct {
	ProtocolInfo   JettingProtocolInfo   `json:"protocol_info"`
	Equipment      JettingEquipment      `json:"equipment"`
	Measurements   JettingMeasurements   `json:"measurements"`
	ExportMetadata JettingExportMetadata `json:"export_metadata"`
}

// JettingProtocolInfo contains header information from the Jetting protocol
type JettingProtocolInfo struct {
	System          string  `json:"system"`           // Jetting System
	DocumentType    string  `json:"document_type"`    // Jetting Protokoll
	Date            string  `json:"date"`             // 2025-10-28
	StartTime       string  `json:"start_time"`       // 13:41
	ProjectNumber   *string `json:"project_number"`   // null for Jetting
	SectionNVT      string  `json:"section_nvt"`      // Dammstr 8 / NVT 1V2300
	Company         string  `json:"company"`          // Wolken-ASM GMBH
	ServiceProvider string  `json:"service_provider"` // M.A.X. Bauservice
	Operator        *string `json:"operator"`         // null for Jetting
	Remarks         string  `json:"remarks"`
}

// JettingEquipment contains equipment specifications (mostly null for Jetting)
type JettingEquipment struct {
	BlowingDevice JettingBlowingDevice `json:"blowing_device"`
	Pipe          JettingPipe          `json:"pipe"`
	Cable         JettingCable         `json:"cable"`
	Compressor    JettingCompressor    `json:"compressor"`
}

// JettingBlowingDevice represents Jetting blowing device (minimal data)
type JettingBlowingDevice struct {
	Model                *string `json:"model"`
	ControllerSN         *string `json:"controller_sn"`
	Lubricator           *bool   `json:"lubricator"`
	CrashTestPerformed   *bool   `json:"crash_test_performed"`
	CrashTestSpeed       *string `json:"crash_test_speed"`
	CrashTestMoment      *string `json:"crash_test_moment"`
}

// JettingPipe represents Jetting pipe specifications (minimal data)
type JettingPipe struct {
	Manufacturer *string   `json:"manufacturer"`
	PipeBundle   *string   `json:"pipe_bundle"`
	PipeType     *string   `json:"pipe_type"`
	ColorCoding  []string  `json:"color_coding"`
	InnerWall    *string   `json:"inner_wall"`
	Temperature  *float64  `json:"temperature"`
}

// JettingCable represents Jetting cable specifications (minimal data)
type JettingCable struct {
	Manufacturer *string  `json:"manufacturer"`
	Designation  *string  `json:"designation"`
	FiberCount   *int     `json:"fiber_count"`
	Diameter     *float64 `json:"diameter"`
	Temperature  *float64 `json:"temperature"`
	Lubricant    *string  `json:"lubricant"`
	BlowingCap   *bool    `json:"blowing_cap"`
}

// JettingCompressor represents Jetting compressor specifications (minimal data)
type JettingCompressor struct {
	Model        *string `json:"model"`
	OilSeparator *bool   `json:"oil_separator"`
	AfterCooler  *bool   `json:"after_cooler"`
}

// JettingMeasurements contains Jetting measurement data
type JettingMeasurements struct {
	MeterReadings JettingMeterReadings `json:"meter_readings"`
	Summary       JettingSummary       `json:"summary"`
	DataPoints    []JettingDataPoint   `json:"data_points"`
}

// JettingMeterReadings represents meter readings (minimal for Jetting)
type JettingMeterReadings struct {
	Start *int `json:"start"`
	End   *int `json:"end"`
}

// JettingSummary contains summary information (minimal for Jetting)
type JettingSummary struct {
	Distance    *int                   `json:"distance"`
	BlowingTime *string                `json:"blowing_time"`
	Weather     JettingWeather         `json:"weather"`
	GPSLocation JettingGPSLocation     `json:"gps_location"`
}

// JettingWeather represents weather conditions (minimal for Jetting)
type JettingWeather struct {
	Temperature *float64 `json:"temperature"`
	Humidity    *float64 `json:"humidity"`
}

// JettingGPSLocation represents GPS coordinates (minimal for Jetting)
type JettingGPSLocation struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

// JettingDataPoint represents individual Jetting measurement point
type JettingDataPoint struct {
	LengthM      float64 `json:"length_m"`      // Länge[m]
	TemperatureC float64 `json:"temperature_c"` // Lufttemperatur[°C]
	ForceN       float64 `json:"force_n"`       // Schubkraft[N]
	PressureBar  float64 `json:"pressure_bar"`  // Einblasdruck[bar]
	SpeedMMin    float64 `json:"speed_m_min"`   // Geschwindigkeit[m/min]
	TimeDuration string  `json:"time_duration"` // Zeit - Dauer[hh:mm:ss]
}

// JettingExportMetadata contains export/parsing metadata
type JettingExportMetadata struct {
	ParsedAt       time.Time `json:"parsed_at"`
	ParserVersion  string    `json:"parser_version"`
	SourceFilename string    `json:"source_filename"`
}