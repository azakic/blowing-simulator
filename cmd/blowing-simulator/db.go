package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

var db *sqlx.DB

// --- Main Entities ---

type Report struct {
	ID      int64  `db:"id"`
	Date    string `db:"date"`
	Address string `db:"address"`
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
		address TEXT,
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
func InsertReport(date, address, typ string) (int64, error) {
	dbType := os.Getenv("DB_TYPE")
	var res sqlx.Result
	var err error
	if dbType == "postgres" {
		res, err = db.Exec("INSERT INTO reports (date, address, type) VALUES ($1, $2, $3)", date, address, typ)
	} else {
		func InsertReport(date, address, typ string) (int64, error) {
			dbType := os.Getenv("DB_TYPE")
			var res sqlx.Result
			var err error
			if dbType == "postgres" {
				res, err = db.Exec("INSERT INTO reports (date, address, type) VALUES ($1, $2, $3)", date, address, typ)
			} else {
				res, err = db.Exec("INSERT INTO reports (date, address, type) VALUES (?, ?, ?)", date, address, typ)
			}
			if err != nil {
				return 0, err
			}
			return res.LastInsertId()
		}

// InsertMeasurement inserts a new measurement for a report.
func InsertMeasurement(reportID int64, length, speed, pressure, torque float64) error {
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
}

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
