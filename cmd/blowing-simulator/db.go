package main

import (
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// JettingProtocol struct definition (restored)
type JettingProtocol struct {
	ID                int64   `db:"id"`
	ReportID          int64   `db:"report_id"`
	Bauvorhaben       string  `db:"bauvorhaben"`
	Streckenabschnitt string  `db:"streckenabschnitt"`
	Firma             string  `db:"firma"`
	Einblaeser        string  `db:"einblaeser"`
	Bemerkungen       string  `db:"bemerkungen"`
	GPS               string  `db:"gps"`
	Datum             string  `db:"datum"`
	Uhrzeit           string  `db:"uhrzeit"`
	RohrHersteller    string  `db:"rohr_hersteller"`
	KabelHersteller   string  `db:"kabel_hersteller"`
	Rohrtyp           string  `db:"rohrtyp"`
	Kabeltyp          string  `db:"kabeltyp"`
	Gleitmittel       string  `db:"gleitmittel"`
	StartMeter        float64 `db:"start_meter"`
	EndMeter          float64 `db:"end_meter"`
	Luftfeuchtigkeit  string  `db:"luftfeuchtigkeit"`
	Temperatur        string  `db:"temperatur"`
	Wetter            string  `db:"wetter"`
	Wind              string  `db:"wind"`
	Druck             string  `db:"druck"`
	Schubkraft        string  `db:"schubkraft"`
	Laenge            string  `db:"laenge"`
	Gleitmittelmenge  string  `db:"gleitmittelmenge"`
	// Add more fields as needed
}

var db *sqlx.DB

// --- Main Entities ---

type Report struct {
	ID      int64  `db:"id"`
	Date    string `db:"date"`
	Time    string `db:"time"`
	Address string `db:"address"`
	NVT     string `db:"nvt"`
	Type    string `db:"type"` // "jetting", "fremco", etc.
}

type Measurement struct {
	ID       int64   `db:"id"`
	ReportID int64   `db:"report_id"`
	Length   float64 `db:"length"`
	Speed    float64 `db:"speed"`
	Pressure float64 `db:"pressure"`
	Torque   float64 `db:"torque"`
}

// InitDB initializes the database connection for SQLite or PostgreSQL based on environment variables.
func InitDB() error {
	dbType := os.Getenv("DB_TYPE") // "sqlite" or "postgres"
	var dsn string

	switch dbType {
	case "sqlite":
		dsn = "blowing.db" // SQLite file
		var err error
		db, err = sqlx.Open("sqlite3", dsn)
		if err != nil {
			return err
		}
	case "postgres":
		// Example DSN: "user=postgres password=secret dbname=blowing sslmode=disable"
		dsn = os.Getenv("PG_DSN")
		var err error
		db, err = sqlx.Open("postgres", dsn)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown DB_TYPE: %s", dbType)
	}
	return db.Ping()
}

// Migrate creates the reports and measurements tables if they do not exist.
func Migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS reports (
		id SERIAL PRIMARY KEY,
		date TEXT,
		time TEXT,
		address TEXT,
		nvt TEXT,
		type TEXT
	);

	CREATE TABLE IF NOT EXISTS measurements (
		id SERIAL PRIMARY KEY,
		report_id INTEGER REFERENCES reports(id),
		length REAL,
		speed REAL,
		pressure REAL,
		torque REAL
	);

	CREATE TABLE IF NOT EXISTS jetting_protocols (
		id SERIAL PRIMARY KEY,
		report_id INTEGER REFERENCES reports(id),
		bauvorhaben TEXT,
		streckenabschnitt TEXT,
		firma TEXT,
		einblaeser TEXT,
		bemerkungen TEXT,
		gps TEXT,
		datum TEXT,
		uhrzeit TEXT,
		rohr_hersteller TEXT,
		kabel_hersteller TEXT,
		rohrtyp TEXT,
		kabeltyp TEXT,
		gleitmittel TEXT,
		start_meter REAL,
		end_meter REAL,
		luftfeuchtigkeit TEXT,
		temperatur TEXT,
		wetter TEXT,
		wind TEXT,
		druck TEXT,
		schubkraft TEXT,
		laenge TEXT,
		gleitmittelmenge TEXT
		-- Add more fields as needed
	);
	`
	_, err := db.Exec(schema)
	return err
}

// InsertReport inserts a new report and returns its ID.
func InsertReport(date, time, address, nvt, typ string) (int64, error) {
	if db == nil {
		fmt.Printf("[MOCK] InsertReport: date=%s, time=%s, address=%s, nvt=%s, type=%s\n", date, time, address, nvt, typ)
		return 1, nil // mock ID
	}
	dbType := os.Getenv("DB_TYPE")
	var err error
	if dbType == "postgres" {
		var id int64
		err = db.QueryRow("INSERT INTO reports (date, time, address, nvt, type) VALUES ($1, $2, $3, $4, $5) RETURNING id", date, time, address, nvt, typ).Scan(&id)
		if err != nil {
			return 0, err
		}
		return id, nil
	} else {
		res, err := db.Exec("INSERT INTO reports (date, time, address, nvt, type) VALUES (?, ?, ?, ?, ?)", date, time, address, nvt, typ)
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		return id, nil
	}
}

// InsertMeasurement inserts a new measurement for a report.
func InsertMeasurement(reportID int64, length, speed, pressure, torque float64) error {
	if db == nil {
		fmt.Printf("[MOCK] InsertMeasurement: reportID=%d, length=%.2f, speed=%.2f, pressure=%.2f, torque=%.2f\n", reportID, length, speed, pressure, torque)
		return nil
	}
	dbType := os.Getenv("DB_TYPE")
	var err error
	if dbType == "postgres" {
		_, err = db.Exec("INSERT INTO measurements (report_id, length, speed, pressure, torque) VALUES ($1, $2, $3, $4, $5)",
			reportID, length, speed, pressure, torque)
	} else {
		_, err = db.Exec("INSERT INTO measurements (report_id, length, speed, pressure, torque) VALUES (?, ?, ?, ?, ?)",
			reportID, length, speed, pressure, torque)
	}
	return err
}

// --- Jetting Protocol Struct, Migration, and Insert ---

func MigrateJettingProtocol() error {
	schema := `
	CREATE TABLE IF NOT EXISTS jetting_protocols (
		id SERIAL PRIMARY KEY,
		bauvorhaben TEXT,
		streckenabschnitt TEXT,
		firma TEXT,
		einblaeser TEXT,
		bemerkungen TEXT,
		gps TEXT,
		datum TEXT,
		uhrzeit TEXT,
		rohr_hersteller TEXT,
		kabel_hersteller TEXT,
		rohrtyp TEXT,
		kabeltyp TEXT,
		gleitmittel TEXT,
		start_meter REAL,
		end_meter REAL,
		luftfeuchtigkeit TEXT,
		temperatur TEXT,
		wetter TEXT,
		wind TEXT,
		druck TEXT,
		schubkraft TEXT,
		laenge TEXT,
		gleitmittelmenge TEXT
		-- Add more fields as needed
	);
	`
	_, err := db.Exec(schema)
	return err
}

func InsertJettingProtocol(p JettingProtocol) error {
	if db == nil {
		fmt.Printf("[MOCK] InsertJettingProtocol: %+v\n", p)
		return nil
	}
	dbType := os.Getenv("DB_TYPE")
	var err error
	query := `
	       INSERT INTO jetting_protocols (
		       report_id, bauvorhaben, streckenabschnitt, firma, einblaeser, bemerkungen, gps, datum, uhrzeit,
		       rohr_hersteller, kabel_hersteller, rohrtyp, kabeltyp, gleitmittel,
		       start_meter, end_meter, luftfeuchtigkeit, temperatur, wetter, wind, druck, schubkraft, laenge, gleitmittelmenge
	       ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       `
	if dbType == "postgres" {
		query = `
		       INSERT INTO jetting_protocols (
			       report_id, bauvorhaben, streckenabschnitt, firma, einblaeser, bemerkungen, gps, datum, uhrzeit,
			       rohr_hersteller, kabel_hersteller, rohrtyp, kabeltyp, gleitmittel,
			       start_meter, end_meter, luftfeuchtigkeit, temperatur, wetter, wind, druck, schubkraft, laenge, gleitmittelmenge
		       ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
	       `
	}
	_, err = db.Exec(query,
		p.ReportID, p.Bauvorhaben, p.Streckenabschnitt, p.Firma, p.Einblaeser, p.Bemerkungen, p.GPS, p.Datum, p.Uhrzeit,
		p.RohrHersteller, p.KabelHersteller, p.Rohrtyp, p.Kabeltyp, p.Gleitmittel,
		p.StartMeter, p.EndMeter, p.Luftfeuchtigkeit, p.Temperatur, p.Wetter, p.Wind, p.Druck, p.Schubkraft, p.Laenge, p.Gleitmittelmenge)
	return err
}
