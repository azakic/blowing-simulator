package simulator

import "strings"

// ApplyJettingDetails upisuje OCR detalje u Jetting protokol. Prazne vrednosti
// ne brišu postojeće.
func ApplyJettingDetails(p *JettingProtocol, d JettingDetails) {
	if p == nil {
		return
	}
	strPtr := func(v string) *string {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		return &v
	}
	set := func(dst **string, v string) {
		if p := strPtr(v); p != nil {
			*dst = p
		}
	}

	set(&p.Equipment.Pipe.Manufacturer, d.PipeManufacturer)
	set(&p.Equipment.Pipe.PipeType, d.PipeType)
	set(&p.Equipment.Pipe.InnerWall, d.PipeInnerWall)
	set(&p.Equipment.Pipe.LayingType, d.PipeLayingType)
	if len(d.PipeColors) > 0 {
		p.Equipment.Pipe.ColorCoding = d.PipeColors
	}
	if d.PipeInnerDia != nil {
		p.Equipment.Pipe.InnerDiameter = d.PipeInnerDia
	}
	if d.PipeOuterDia != nil {
		p.Equipment.Pipe.OuterDiameter = d.PipeOuterDia
	}
	if d.PipeTemp != nil {
		p.Equipment.Pipe.Temperature = d.PipeTemp
	}

	set(&p.Equipment.Cable.Manufacturer, d.CableManufacturer)
	set(&p.Equipment.Cable.Designation, d.CableDesignation)
	set(&p.Equipment.Cable.DrumNo, d.CableDrumNo)
	if d.CableDiameter != nil {
		p.Equipment.Cable.Diameter = d.CableDiameter
	}
	if d.CableFiberCount != nil {
		p.Equipment.Cable.FiberCount = d.CableFiberCount
	}
	if d.CableTemp != nil {
		p.Equipment.Cable.Temperature = d.CableTemp
	}
	set(&p.Equipment.Cable.Lubricant, d.Lubricant)

	set(&p.Equipment.BlowingDevice.Model, d.DeviceModel)
	set(&p.Equipment.Compressor.Model, d.CompressorModel)

	if d.BlowingTime != "" {
		t := d.BlowingTime
		p.Measurements.Summary.BlowingTime = &t
	}
	if d.Distance != nil {
		p.Measurements.Summary.Distance = d.Distance
	}
	if d.MeterStart != nil {
		p.Measurements.MeterReadings.Start = d.MeterStart
	}
	if d.MeterEnd != nil {
		p.Measurements.MeterReadings.End = d.MeterEnd
	}
	if d.WeatherTemp != nil {
		p.Measurements.Summary.Weather.Temperature = d.WeatherTemp
	}
	if d.WeatherHumid != nil {
		p.Measurements.Summary.Weather.Humidity = d.WeatherHumid
	}
	if d.GPSLat != nil {
		p.Measurements.Summary.GPSLocation.Latitude = d.GPSLat
	}
	if d.GPSLon != nil {
		p.Measurements.Summary.GPSLocation.Longitude = d.GPSLon
	}
}
