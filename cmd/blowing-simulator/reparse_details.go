package main

import (
	"log"

	"blowing-simulator/internal/simulator"
	"github.com/lib/pq"
)

// updateParsedDetails osvežava `protocol_equipment` i `protocol_summary` iz
// ponovo parsiranog dokumenta. Merenja se i dalje ne diraju.
//
// Pravilo je isto kao za metapodatke: **prazno ne briše postojeće**. Tako
// re-parse popravlja pogrešne vrednosti (npr. `pipe_bundle` koji je bio
// odsečen na prvoj reči), a ne može da obriše ništa.
func updateParsedDetails(id int, loaded interface{}) {
	switch p := loaded.(type) {
	case *simulator.FremcoProtocol:
		updateFremcoDetails(id, p)
	case *simulator.JettingProtocol:
		// Jetting parser trenutno ne popunjava opremu/summary (podaci postoje
		// samo kao slika u zaglavlju), pa nema šta da se upiše.
	}
}

func updateFremcoDetails(id int, p *simulator.FremcoProtocol) {
	e := p.Equipment
	m := p.Measurements

	var colors interface{}
	if len(e.Pipe.ColorCoding) > 0 {
		colors = pq.Array(e.Pipe.ColorCoding)
	}

	_, err := db.Exec(`
		UPDATE protocol_equipment SET
			device_model          = COALESCE(NULLIF($2, ''), device_model),
			controller_sn         = COALESCE(NULLIF($3, ''), controller_sn),
			lubricator            = COALESCE($4, lubricator),
			crash_test_performed  = COALESCE($5, crash_test_performed),
			pipe_manufacturer     = COALESCE(NULLIF($6, ''), pipe_manufacturer),
			pipe_bundle           = COALESCE(NULLIF($7, ''), pipe_bundle),
			pipe_type             = COALESCE(NULLIF($8, ''), pipe_type),
			pipe_color_coding     = COALESCE($9, pipe_color_coding),
			pipe_inner_wall       = COALESCE(NULLIF($10, ''), pipe_inner_wall),
			pipe_temperature      = COALESCE($11, pipe_temperature),
			cable_manufacturer    = COALESCE(NULLIF($12, ''), cable_manufacturer),
			cable_designation     = COALESCE(NULLIF($13, ''), cable_designation),
			cable_fiber_count     = COALESCE($14, cable_fiber_count),
			cable_diameter        = COALESCE($15, cable_diameter),
			cable_temperature     = COALESCE($16, cable_temperature),
			cable_lubricant       = COALESCE(NULLIF($17, ''), cable_lubricant),
			cable_blowing_cap     = COALESCE($18, cable_blowing_cap),
			compressor_model      = COALESCE(NULLIF($19, ''), compressor_model),
			compressor_oil_separator = COALESCE($20, compressor_oil_separator),
			compressor_after_cooler  = COALESCE($21, compressor_after_cooler)
		WHERE protocol_id = $1`,
		id,
		e.BlowingDevice.Model,
		e.BlowingDevice.ControllerSN,
		boolOrNil(e.BlowingDevice.Lubricator),
		boolOrNil(e.BlowingDevice.CrashTestPerformed),
		e.Pipe.Manufacturer,
		e.Pipe.PipeBundle,
		e.Pipe.PipeType,
		colors,
		e.Pipe.InnerWall,
		floatOrNil(e.Pipe.Temperature),
		e.Cable.Manufacturer,
		e.Cable.Designation,
		intOrNil(e.Cable.FiberCount),
		floatOrNil(&e.Cable.Diameter),
		floatOrNil(e.Cable.Temperature),
		e.Cable.Lubricant,
		boolOrNil(e.Cable.BlowingCap),
		e.Compressor.Model,
		boolOrNil(e.Compressor.OilSeparator),
		boolOrNil(e.Compressor.AfterCooler),
	)
	if err != nil {
		log.Printf("reparse %d: equipment update: %v", id, err)
	}

	_, err = db.Exec(`
		UPDATE protocol_summary SET
			meter_start         = COALESCE($2, meter_start),
			meter_end           = COALESCE($3, meter_end),
			total_distance      = COALESCE($4, total_distance),
			blowing_time        = COALESCE($5, blowing_time),
			weather_temperature = COALESCE($6, weather_temperature),
			weather_humidity    = COALESCE($7, weather_humidity),
			gps_latitude        = COALESCE($8, gps_latitude),
			gps_longitude       = COALESCE($9, gps_longitude)
		WHERE protocol_id = $1`,
		id,
		intOrNil(m.MeterReadings.Start),
		intOrNil(m.MeterReadings.End),
		intOrNil(m.Summary.Distance),
		parseDuration(m.Summary.BlowingTime),
		floatOrNil(&m.Summary.Weather.Temperature),
		floatOrNil(&m.Summary.Weather.Humidity),
		floatOrNil(&m.Summary.GPSLocation.Latitude),
		floatOrNil(&m.Summary.GPSLocation.Longitude),
	)
	if err != nil {
		log.Printf("reparse %d: summary update: %v", id, err)
	}
}

func boolOrNil(v bool) interface{} {
	if !v {
		return nil
	}
	return v
}

func floatOrNil(v *float64) interface{} {
	if v == nil || *v == 0 {
		return nil
	}
	return *v
}

func intOrNil(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
