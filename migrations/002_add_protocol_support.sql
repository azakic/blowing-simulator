-- Enhanced schema for comprehensive Fremco and Jetting protocol storage
-- Run this migration to add protocol support: 002_add_protocol_support.sql

-- Protocol main table
CREATE TABLE protocols (
    id SERIAL PRIMARY KEY,
    protocol_type VARCHAR(20) NOT NULL CHECK (protocol_type IN ('fremco', 'jetting')),
    system_name VARCHAR(100),
    document_type VARCHAR(100),
    protocol_date DATE,
    start_time TIME,
    project_number VARCHAR(50),
    section_nvt VARCHAR(200),
    company VARCHAR(200),
    service_provider VARCHAR(200),
    operator VARCHAR(100),
    remarks TEXT,
    source_filename VARCHAR(500),
    parsed_at TIMESTAMP DEFAULT NOW(),
    parser_version VARCHAR(20) DEFAULT '1.0.0',
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Equipment specifications table
CREATE TABLE protocol_equipment (
    id SERIAL PRIMARY KEY,
    protocol_id INTEGER REFERENCES protocols(id) ON DELETE CASCADE,
    
    -- Blowing Device
    device_model VARCHAR(200),
    controller_sn VARCHAR(100),
    lubricator BOOLEAN,
    crash_test_performed BOOLEAN,
    crash_test_speed VARCHAR(50),
    crash_test_moment VARCHAR(50),
    
    -- Pipe specifications
    pipe_manufacturer VARCHAR(200),
    pipe_bundle VARCHAR(100),
    pipe_type VARCHAR(100),
    pipe_color_coding TEXT[], -- Array of colors
    pipe_inner_wall VARCHAR(100),
    pipe_temperature DECIMAL(5,2),
    
    -- Cable specifications  
    cable_manufacturer VARCHAR(200),
    cable_designation VARCHAR(200),
    cable_fiber_count INTEGER,
    cable_diameter DECIMAL(5,2),
    cable_temperature DECIMAL(5,2),
    cable_lubricant VARCHAR(200),
    cable_blowing_cap BOOLEAN,
    
    -- Compressor specifications
    compressor_model VARCHAR(200),
    compressor_oil_separator BOOLEAN,
    compressor_after_cooler BOOLEAN,
    
    created_at TIMESTAMP DEFAULT NOW()
);

-- Protocol summary and metadata
CREATE TABLE protocol_summary (
    id SERIAL PRIMARY KEY,
    protocol_id INTEGER REFERENCES protocols(id) ON DELETE CASCADE,
    
    -- Meter readings
    meter_start INTEGER,
    meter_end INTEGER,
    
    -- Summary data
    total_distance INTEGER,
    blowing_time INTERVAL,
    
    -- Weather conditions
    weather_temperature DECIMAL(5,2),
    weather_humidity DECIMAL(5,2),
    
    -- GPS location
    gps_latitude DECIMAL(10,8),
    gps_longitude DECIMAL(11,8),
    
    created_at TIMESTAMP DEFAULT NOW()
);

-- Individual measurement data points
CREATE TABLE protocol_measurements (
    id SERIAL PRIMARY KEY,
    protocol_id INTEGER REFERENCES protocols(id) ON DELETE CASCADE,
    
    -- Common fields for both Fremco and Jetting
    length_m DECIMAL(8,2),
    timestamp_value TIMESTAMP,
    
    -- Fremco specific fields
    speed_m_min DECIMAL(8,2),        -- Geschwindigkeit [m/min]
    pressure_bar DECIMAL(8,3),       -- Rohr-Druck [bar] / Einblasdruck[bar]
    torque_percent DECIMAL(6,2),     -- Drehmoment [%] (Fremco only)
    
    -- Jetting specific fields
    temperature_c DECIMAL(5,2),      -- Lufttemperatur[°C] (Jetting only)
    force_n DECIMAL(8,2),            -- Schubkraft[N] (Jetting only)
    time_duration INTERVAL,          -- Zeit - Dauer[hh:mm:ss] (Jetting only)
    
    -- Sequence for ordering
    sequence_number INTEGER,
    
    created_at TIMESTAMP DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_protocols_type ON protocols(protocol_type);
CREATE INDEX idx_protocols_date ON protocols(protocol_date);
CREATE INDEX idx_protocols_project ON protocols(project_number);
CREATE INDEX idx_protocol_measurements_protocol_id ON protocol_measurements(protocol_id);
CREATE INDEX idx_protocol_measurements_sequence ON protocol_measurements(protocol_id, sequence_number);

-- Views for easier querying

-- Fremco protocols with summary
CREATE VIEW fremco_protocols_view AS
SELECT 
    p.*,
    ps.total_distance,
    ps.blowing_time,
    ps.weather_temperature,
    ps.weather_humidity,
    ps.gps_latitude,
    ps.gps_longitude,
    pe.device_model,
    pe.cable_manufacturer,
    pe.cable_designation
FROM protocols p
LEFT JOIN protocol_summary ps ON p.id = ps.protocol_id
LEFT JOIN protocol_equipment pe ON p.id = pe.protocol_id
WHERE p.protocol_type = 'fremco';

-- Jetting protocols with summary
CREATE VIEW jetting_protocols_view AS
SELECT 
    p.*,
    ps.total_distance,
    ps.blowing_time,
    ps.weather_temperature,
    ps.weather_humidity,
    pe.device_model,
    pe.cable_manufacturer,
    pe.cable_designation
FROM protocols p
LEFT JOIN protocol_summary ps ON p.id = ps.protocol_id
LEFT JOIN protocol_equipment pe ON p.id = pe.protocol_id
WHERE p.protocol_type = 'jetting';

-- Protocol measurement count view
CREATE VIEW protocol_measurement_counts AS
SELECT 
    protocol_id,
    COUNT(*) as measurement_count,
    MIN(length_m) as min_length,
    MAX(length_m) as max_length,
    AVG(speed_m_min) as avg_speed,
    AVG(pressure_bar) as avg_pressure
FROM protocol_measurements
GROUP BY protocol_id;