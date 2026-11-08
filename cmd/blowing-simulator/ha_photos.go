package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// --- Storage za HaP priloge ---

// PutRaw upisuje proizvoljan objekat u isti bucket kao originali protokola.
func (s *SourceStore) PutRaw(ctx context.Context, key string, data []byte, contentType string) error {
	if !s.Enabled() {
		return errors.New("object storage is not configured")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}

// GetRaw čita objekat po ključu.
func (s *SourceStore) GetRaw(ctx context.Context, key string) ([]byte, string, error) {
	if !s.Enabled() {
		return nil, "", errors.New("object storage is not configured")
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", err
	}
	ct := "application/octet-stream"
	if out.ContentType != nil && *out.ContentType != "" {
		ct = *out.ContentType
	}
	return data, ct, nil
}

func haPhotoKey(haID int64, kind, ext string) string {
	if ext == "" {
		ext = ".jpg"
	}
	return fmt.Sprintf("ha/%d/%s-%d%s", haID, kind, time.Now().UnixNano(), ext)
}

// --- Minimalni EXIF čitač (JPEG + TIFF/EXIF + GPS) ---

// exifInfo su podaci koje koristimo za prikaz ispod fotografije i u PDF-u.
type exifInfo struct {
	Lat, Lon     float64
	Altitude     float64
	Bearing      float64
	TakenAt      time.Time
	HasLocation  bool
	HasTimestamp bool
	Device       string
}

type exifEntry struct {
	tag       uint16
	typ       uint16
	count     uint32
	valueOff  uint32
	valueData []byte
}

// parseExif čita GPS i vreme iz JPEG-a. Vraća praznu strukturu ako EXIF nema.
func parseExif(data []byte) exifInfo {
	var info exifInfo
	app1 := findExifAPP1(data)
	if app1 == nil {
		return info
	}
	// app1 počinje sa "Exif\0\0" pa TIFF header
	tiff := app1[6:]
	if len(tiff) < 8 {
		return info
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return info
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return info
	}
	ifd0 := int(bo.Uint32(tiff[4:8]))
	if ifd0+2 > len(tiff) {
		return info
	}

	entries0 := readIFD(tiff, ifd0, bo)
	var exifOffset, gpsOffset int
	for _, e := range entries0 {
		switch e.tag {
		case 0x0132: // DateTime
			if t, err := parseExifTime(asciiValue(tiff, e, bo)); err == nil && !info.HasTimestamp {
				info.TakenAt, info.HasTimestamp = t, true
			}
		case 0x010F: // Make
			if s := strings.TrimSpace(asciiValue(tiff, e, bo)); s != "" {
				info.Device = s
			}
		case 0x8769: // Exif IFD
			if len(e.valueData) >= 4 {
				exifOffset = int(bo.Uint32(e.valueData))
			}
		case 0x8825: // GPS IFD
			if len(e.valueData) >= 4 {
				gpsOffset = int(bo.Uint32(e.valueData))
			}
		}
	}

	if exifOffset > 0 && exifOffset < len(tiff) {
		for _, e := range readIFD(tiff, exifOffset, bo) {
			switch e.tag {
			case 0x9003, 0x9004: // DateTimeOriginal / DateTimeDigitized
				if t, err := parseExifTime(asciiValue(tiff, e, bo)); err == nil {
					info.TakenAt, info.HasTimestamp = t, true
				}
			}
		}
	}

	if gpsOffset > 0 && gpsOffset < len(tiff) {
		var latRef, lonRef string
		var lat, lon, alt, bearing float64
		var hasLat, hasLon, hasAlt, hasBearing bool
		for _, e := range readIFD(tiff, gpsOffset, bo) {
			switch e.tag {
			case 0x0001: // GPSLatitudeRef
				latRef = strings.ToUpper(strings.TrimSpace(asciiValue(tiff, e, bo)))
			case 0x0002: // GPSLatitude
				lat, hasLat = rationalTriplet(tiff, e, bo)
			case 0x0003: // GPSLongitudeRef
				lonRef = strings.ToUpper(strings.TrimSpace(asciiValue(tiff, e, bo)))
			case 0x0004: // GPSLongitude
				lon, hasLon = rationalTriplet(tiff, e, bo)
			case 0x0005:
				// 0 = iznad mora, 1 = ispod
			case 0x0006:
				alt, hasAlt = rationalSingle(tiff, e, bo)
			case 0x0011:
				bearing, hasBearing = rationalSingle(tiff, e, bo)
			}
		}
		if hasLat && hasLon {
			if latRef == "S" {
				lat = -lat
			}
			if lonRef == "W" {
				lon = -lon
			}
			info.Lat, info.Lon, info.HasLocation = lat, lon, true
		}
		if hasAlt {
			info.Altitude = alt
		}
		if hasBearing {
			info.Bearing = bearing
		}
	}
	return info
}

// findExifAPP1 pronalazi APP1 segment sa "Exif\0\0" i vraća njegov sadržaj
// POSLE zaglavlja segmenta (uključujući "Exif\0\0").
func findExifAPP1(data []byte) []byte {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return nil
		}
		marker := data[i+1]
		if marker == 0xD9 || marker == 0xDA { // EOI / SOS
			return nil
		}
		// markeri bez dužine
		if marker >= 0xD0 && marker <= 0xD7 || marker == 0x01 {
			i += 2
			continue
		}
		segLen := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if segLen < 2 || i+2+segLen > len(data) {
			return nil
		}
		payload := data[i+4 : i+2+segLen]
		if marker == 0xE1 && len(payload) >= 6 && string(payload[:4]) == "Exif" {
			return payload
		}
		i += 2 + segLen
	}
	return nil
}

func readIFD(tiff []byte, offset int, bo binary.ByteOrder) []exifEntry {
	if offset+2 > len(tiff) {
		return nil
	}
	n := int(bo.Uint16(tiff[offset : offset+2]))
	pos := offset + 2
	out := make([]exifEntry, 0, n)
	for i := 0; i < n; i++ {
		if pos+12 > len(tiff) {
			break
		}
		e := exifEntry{
			tag:      bo.Uint16(tiff[pos : pos+2]),
			typ:      bo.Uint16(tiff[pos+2 : pos+4]),
			count:    bo.Uint32(tiff[pos+4 : pos+8]),
			valueOff: bo.Uint32(tiff[pos+8 : pos+12]),
		}
		size := typeSize(e.typ) * int(e.count)
		if size <= 4 {
			e.valueData = tiff[pos+8 : pos+8+size]
		} else if int(e.valueOff)+size <= len(tiff) {
			e.valueData = tiff[e.valueOff : int(e.valueOff)+size]
		}
		out = append(out, e)
		pos += 12
	}
	return out
}

func typeSize(typ uint16) int {
	switch typ {
	case 1, 2, 6, 7: // BYTE, ASCII, SBYTE, UNDEFINED
		return 1
	case 3, 8: // SHORT, SSHORT
		return 2
	case 4, 9, 11: // LONG, SLONG, FLOAT
		return 4
	case 5, 10, 12: // RATIONAL, SRATIONAL, DOUBLE
		return 8
	}
	return 1
}

func asciiValue(tiff []byte, e exifEntry, bo binary.ByteOrder) string {
	if e.typ != 2 || len(e.valueData) == 0 {
		return ""
	}
	s := string(bytes.TrimRight(e.valueData, "\x00"))
	return s
}

func rationalTriplet(tiff []byte, e exifEntry, bo binary.ByteOrder) (float64, bool) {
	if len(e.valueData) < 24 {
		return 0, false
	}
	var vals [3]float64
	for i := 0; i < 3; i++ {
		num := bo.Uint32(e.valueData[i*8 : i*8+4])
		den := bo.Uint32(e.valueData[i*8+4 : i*8+8])
		if den == 0 {
			return 0, false
		}
		vals[i] = float64(num) / float64(den)
	}
	return vals[0] + vals[1]/60 + vals[2]/3600, true
}

func rationalSingle(tiff []byte, e exifEntry, bo binary.ByteOrder) (float64, bool) {
	if len(e.valueData) < 8 {
		return 0, false
	}
	num := bo.Uint32(e.valueData[0:4])
	den := bo.Uint32(e.valueData[4:8])
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

func parseExifTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006:01:02 15:04:05", "2006:01:02 15:04"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("exif time")
}

// formatCoord prikazuje koordinate u obliku koji koriste terenske aplikacije.
func formatCoord(lat, lon float64) string {
	ns, ew := "N", "E"
	if lat < 0 {
		ns, lat = "S", -lat
	}
	if lon < 0 {
		ew, lon = "W", -lon
	}
	return fmt.Sprintf("%s %s %s %s", degToDMS(lat), ns, degToDMS(lon), ew)
}

func degToDMS(v float64) string {
	d := int(v)
	mf := (v - float64(d)) * 60
	m := int(mf)
	s := (mf - float64(m)) * 60
	return fmt.Sprintf("%d°%d'%.1f\"", d, m, math.Round(s*10)/10)
}

func formatExifCaption(info exifInfo) string {
	var parts []string
	if info.HasTimestamp {
		parts = append(parts, info.TakenAt.Format("02.01.2006 15:04"))
	}
	if info.HasLocation {
		parts = append(parts, fmt.Sprintf("%.5f, %.5f", info.Lat, info.Lon))
		if info.Altitude != 0 {
			parts = append(parts, fmt.Sprintf("%.0f m", info.Altitude))
		}
	}
	return strings.Join(parts, " · ")
}

// parseLatLonText prihvata "51.3618, 7.3684" iz forme (ručni unos).
func parseLatLonText(s string) (lat, lon float64, ok bool) {
	parts := strings.Split(strings.ReplaceAll(s, ";", ","), ",")
	if len(parts) != 2 {
		return 0, 0, false
	}
	la, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lo, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || math.Abs(la) > 90 || math.Abs(lo) > 180 {
		return 0, 0, false
	}
	return la, lo, true
}
