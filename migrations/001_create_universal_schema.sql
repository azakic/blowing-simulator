-- Migration: Create universal schema for Jetting and Fremco reports

CREATE TABLE IF NOT EXISTS reports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT,
    project_number TEXT,
    project TEXT,
    date TEXT,
    time TEXT,
    address TEXT,
    nvt TEXT,
    company TEXT,
    device TEXT,
    cable TEXT,
    meter_range TEXT,
    weather TEXT,
    gps TEXT,
    filename TEXT,
    created_at DATETIME
);

CREATE TABLE IF NOT EXISTS measurements (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    report_id INTEGER,
    length REAL,
    speed REAL,
    pressure REAL,
    torque REAL,
    time TEXT,
    FOREIGN KEY(report_id) REFERENCES reports(id)
);
