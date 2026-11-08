package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// .ics izvoz zadatka — radnik na telefonu tapne „Add to calendar“ i dobije
// događaj sa adresom, NVT-om i rokom. Vreme se emituje kao **lokalno
// (floating)** jer se radovi izvode u istoj vremenskoj zoni; tako kalendar na
// telefonu prikaže tačan termin bez TZ konverzije.
const icsProdID = "-//Wolken-ASM//Reports Tasks//EN"

// TaskICSHandler služi `/tasks/ics?id=N` kao `.ics` fajl.
func TaskICSHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next="+urlQuery(r.URL.String()), http.StatusFound)
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 {
		http.Error(w, "Missing task id", http.StatusBadRequest)
		return
	}
	t, err := loadTask(id)
	if err != nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}
	if !canSeeTask(r, t) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !t.DueDate.Valid || t.DueDate.String == "" {
		http.Error(w, "Task has no due date — add a date (and time) and try again.", http.StatusBadRequest)
		return
	}

	ics := buildTaskICS(t, absoluteURL(r, "/tasks/view?id="+strconv.FormatInt(t.ID, 10)))

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="task-%d.ics"`, t.ID))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(ics)
}

// buildTaskICS pravi VCALENDAR sa jednim VEVENT-om.
func buildTaskICS(t taskRow, taskURL string) []byte {
	var b strings.Builder
	write := func(line string) {
		b.WriteString(foldICSLines(line))
	}

	write("BEGIN:VCALENDAR")
	write("VERSION:2.0")
	write("PRODID:" + icsProdID)
	write("CALSCALE:GREGORIAN")
	write("METHOD:PUBLISH")
	write("BEGIN:VEVENT")
	write(fmt.Sprintf("UID:task-%d@ha.wolken-asm.de", t.ID))
	write("DTSTAMP:" + time.Now().UTC().Format("20060102T150405Z"))

	// Datum dolazi kao YYYY-MM-DD (to_char), vreme kao HH:MM.
	day := strings.ReplaceAll(t.DueDate.String, "-", "")
	if t.DueTime.Valid && t.DueTime.String != "" {
		hhmm := strings.ReplaceAll(t.DueTime.String, ":", "")
		start := day + "T" + hhmm + "00"
		// Podrazumevano trajanje: 1 h (dovoljno za HA termin).
		end := addHour(day, t.DueTime.String)
		write("DTSTART:" + start)
		write("DTEND:" + end)
	} else {
		write("DTSTART;VALUE=DATE:" + day)
		write("DTEND;VALUE=DATE:" + nextDay(day))
	}

	summary := "HA: " + t.Title
	if t.Project != "" {
		summary += " (" + t.Project + ")"
	}
	write("SUMMARY:" + icsEscape(summary))
	write("STATUS:CONFIRMED")

	desc := []string{"Project: " + t.Project}
	if t.ProjectName != "" {
		desc = append(desc, "Name: "+t.ProjectName)
	}
	if t.NVT != "" {
		desc = append(desc, "NVT: "+t.NVT)
	}
	if t.Address != "" {
		desc = append(desc, "Address: "+t.Address)
	}
	if t.Description != "" {
		desc = append(desc, t.Description)
	}
	desc = append(desc, "Task: "+taskURL)
	write("DESCRIPTION:" + icsEscape(strings.Join(desc, "\n")))

	if t.Address != "" {
		write("LOCATION:" + icsEscape(t.Address))
	}
	write("URL:" + icsEscape(taskURL))

	// Podsetnik 1 h pre termina (samo kad vreme postoji).
	if t.DueTime.Valid && t.DueTime.String != "" {
		write("BEGIN:VALARM")
		write("ACTION:DISPLAY")
		write("DESCRIPTION:" + icsEscape("Task: "+t.Title))
		write("TRIGGER:-PT1H")
		write("END:VALARM")
	}

	write("END:VEVENT")
	write("END:VCALENDAR")
	return []byte(b.String())
}

// addHour vraća DTEND za `day` (YYYYMMDD) i `clock` (HH:MM), +1 h.
func addHour(day, clock string) string {
	t, err := time.Parse("20060102 15:04", day+" "+clock)
	if err != nil {
		return day + "T" + strings.ReplaceAll(clock, ":", "") + "00"
	}
	return t.Add(time.Hour).Format("20060102T150405")
}

func nextDay(day string) string {
	t, err := time.Parse("20060102", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, 1).Format("20060102")
}

// icsEscape escaping po RFC 5545 §3.3.11.
func icsEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		";", `\;`,
		",", `\,`,
		"\r\n", `\n`,
		"\n", `\n`,
		"\r", `\n`,
	)
	return r.Replace(s)
}

// foldICSLines prelama linije na 75 okteta (RFC 5545 §3.1). Prelom ide posle
// CRLF i jednog razmaka, na granici rune (da se ne preseče UTF-8).
func foldICSLines(line string) string {
	const limit = 75
	if len(line) <= limit {
		return line + "\r\n"
	}
	var b strings.Builder
	runes := []rune(line)
	count := 0
	for _, r := range runes {
		rl := len(string(r))
		if count+rl > limit {
			b.WriteString("\r\n ")
			count = 1
		}
		b.WriteRune(r)
		count += rl
	}
	b.WriteString("\r\n")
	return b.String()
}
