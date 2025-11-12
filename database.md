# Blowing Simulator Database Structure

This document describes the database schema, main entities, relationships, and usage for the Blowing Simulator application. The schema is designed to work with both SQLite and PostgreSQL.

---

## Main Entities

### 1. `reports`
Stores metadata about each PDF/report.

| Field    | Type    | Description                       |
|----------|---------|-----------------------------------|
| id       | SERIAL/INTEGER | Unique identifier (primary key) |
| date     | TEXT    | Date of the report                |
| address  | TEXT    | Address/location                  |
| type     | TEXT    | Report type ("jetting", "fremco") |

---

### 2. `measurements`
Stores individual measurement rows, linked to a report.

| Field      | Type    | Description                                 |
|------------|---------|---------------------------------------------|
| id         | SERIAL/INTEGER | Unique identifier (primary key)           |
| report_id  | INTEGER | Foreign key referencing `reports.id`         |
| length     | REAL    | Measured length (Streckenlänge [m])          |
| speed      | REAL    | Measured speed (Geschwindigkeit [m/min])     |
| pressure   | REAL    | Measured pressure (Rohr-Druck [bar])         |
| torque     | REAL    | Measured torque (Drehmoment [%])             |

---

### 3. `jetting_protocols`
Stores protocol data for Jetting reports, linked to a report.

| Field             | Type    | Description                                 |
|-------------------|---------|---------------------------------------------|
| id                | SERIAL/INTEGER | Unique identifier (primary key)           |
| report_id         | INTEGER | Foreign key referencing `reports.id`         |
| bauvorhaben       | TEXT    | Project name/number                         |
| streckenabschnitt | TEXT    | Section info                                |
| firma             | TEXT    | Company                                     |
| einblaeser        | TEXT    | Operator/Blowing device                     |
| bemerkungen       | TEXT    | Comments                                    |
| gps               | TEXT    | GPS location                                |
| datum             | TEXT    | Date                                        |
| uhrzeit           | TEXT    | Time                                        |
| rohr_hersteller   | TEXT    | Pipe manufacturer                           |
| kabel_hersteller  | TEXT    | Cable manufacturer                          |
| rohrtyp           | TEXT    | Pipe type                                   |
| kabeltyp          | TEXT    | Cable type                                  |
| gleitmittel       | TEXT    | Lubricant                                   |
| start_meter       | REAL    | Cable meter mark at start of blowing        |
| end_meter         | REAL    | Cable meter mark at end of blowing          |
| luftfeuchtigkeit  | TEXT    | Humidity                                    |
| temperatur        | TEXT    | Temperature                                 |
| wetter            | TEXT    | Weather                                     |
| wind              | TEXT    | Wind                                        |
| druck             | TEXT    | Pressure                                    |
| schubkraft        | TEXT    | Thrust                                      |
| laenge            | TEXT    | Length                                      |
| gleitmittelmenge  | TEXT    | Lubricant amount                            |
| ...               | ...     | (Add more fields as needed)                 |

---

## Relationships

- **reports (1) <--- (many) measurements**
- **reports (1) <--- (1) jetting_protocols**

---

## Example Go Structs

```go
type Report struct {
    ID      int64  `db:"id"`
    Date    string `db:"date"`
    Address string `db:"address"`
    Type    string `db:"type"`
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
```

---

## Example SQL Migration

```sql
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
```

---

## Usage

- When processing a PDF, create a `Report` entry.
- Insert all `Measurement` rows linked to that report.
- Insert a `JettingProtocol` entry linked to the same report.

---

## Extending the Schema

You can add more fields to any entity as needed (e.g., fiber colors, signatures, user info, etc.).  
This structure supports robust querying, reporting, and future extension for all your Jetting/Fremco workflows.

---