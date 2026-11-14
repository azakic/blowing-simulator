// Prediktivno popunjavanje zadatka iz direktorijuma klijenata (/admin/import).
// Kada se unese adresa (ulica+broj, PLZ, grad) ili NVT, prazna polja se
// popunjavaju: NVT, ime/prezime (ili firma), telefon, mobil, PLZ i grad.
// Postojeće vrednosti se nikad ne prepisuju.
(function () {
  var street = document.getElementById('task-address');
  var postcode = document.getElementById('task-postcode');
  var city = document.getElementById('task-city');
  var nvt = document.getElementById('task-nvt');
  var hint = document.getElementById('task-lookup-hint');
  if (!street || !hint) { return; }
  var timer = null;

  // Polja koja je korisnik dirao ne popunjavamo automatski: kada obriše
  // adresu ili NVT, sledeći lookup ih ne sme vratiti (izgleda kao petlja).
  var touched = {};
  ['task-address', 'task-postcode', 'task-city', 'task-nvt'].forEach(function (id) {
    var el = document.getElementById(id);
    if (el) { el.addEventListener('input', function () { touched[id] = true; }); }
  });

  function fill(el, value) {
    if (!el || !value || touched[el.id] || el.value.trim()) { return false; }
    el.value = value;
    return true;
  }
  function setIfEmpty(el, value) { fill(el, value); }

  // „SM209214964“ → izaberi opciju projekta koja počinje tim brojem.
  function selectProject(number) {
    if (!number) { return; }
    var sel = document.querySelector('select[name="project_id"]');
    if (!sel || sel.value) { return; }
    for (var i = 0; i < sel.options.length; i++) {
      var txt = (sel.options[i].textContent || '').trim();
      if (txt.indexOf(number) === 0) { sel.selectedIndex = i; return; }
    }
  }

  function pick(name) { return document.querySelector('input[name="' + name + '"]'); }
  function val(el) { return el ? (el.value || '').trim() : ''; }

  // Ista adresa koju server razume: „Straße Nr., PLZ Ort“.
  function composedAddress() {
    var line = val(street);
    var loc = (val(postcode) + ' ' + val(city)).trim();
    if (loc) { return line ? line + ', ' + loc : loc; }
    return line;
  }

  // GPS aus dem importierten Protokoll (Fremco/Jetting) → PLZ/Ort.
  // Leere Felder werden gefüllt, bereits eingetragene Werte NIE überschrieben:
  // bei Abweichung erscheint nur ein Hinweis zur manuellen Prüfung.
  function samePlace(a, b) {
    var x = (a || '').toLowerCase().replace(/\s+/g, ' ').trim();
    var y = (b || '').toLowerCase().replace(/\s+/g, ' ').trim();
    if (!x || !y) { return false; }
    return x === y || x.indexOf(y) >= 0 || y.indexOf(x) >= 0;
  }

  function verifyWithGps(pr) {
    if (!pr || !pr.lat || !pr.lon) { return; }
    fetch('/geo/address?lat=' + encodeURIComponent(pr.lat) + '&lon=' + encodeURIComponent(pr.lon),
      { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (g) {
        if (!g || (!g.postcode && !g.city)) { return; }
        var gps = (g.postcode + ' ' + g.city).trim();
        var typed = (val(postcode) + ' ' + val(city)).trim();
        var msg;
        if (!typed) {
          setIfEmpty(postcode, g.postcode);
          setIfEmpty(city, g.city);
          msg = 'PLZ/Ort aus GPS übernommen: ' + gps + '.';
        } else if ((g.postcode && g.postcode !== val(postcode)) ||
                   (g.city && !samePlace(g.city, val(city)))) {
          msg = '⚠ GPS sagt ' + gps + ', im Formular steht ' + typed + ' — bitte prüfen.';
        } else {
          msg = 'GPS bestätigt ' + gps + '.';
        }
        hint.textContent = val(hint.textContent) ? val(hint.textContent) + ' ' + msg : msg;
        hint.style.display = 'block';
      })
      .catch(function () {});
  }

  // Više pogodaka (ista ulica u drugom mestu ili jedan NVT za više adresa):
  // ništa se ne popunjava samo — ponudi izbor, jer je pogrešan klijent gori
  // od praznog polja. Klik popuni adresu i NVT i odmah ponovi lookup.
  function showCandidates(matches) {
    hint.textContent = matches.length > 1
      ? 'Mehrere Einträge (' + matches.length + ') — bitte wählen:'
      : 'Eintrag gefunden — bitte bestätigen:';
    var list = document.createElement('span');
    list.className = 'lookup-choices';
    matches.forEach(function (m) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'lookup-choice';
      var line = ((m.street || '') + ' ' + (m.house_number || '')).trim() || m.address;
      var parts = [line];
      var place = ((m.postcode || '') + ' ' + (m.city || '')).trim();
      if (place) { parts.push(place); }
      if (m.nvt) { parts.push('NVT ' + m.nvt); }
      if (m.kunde) { parts.push(m.kunde); }
      btn.textContent = parts.join(' · ');
      btn.addEventListener('click', function () {
        if (m.street) { street.value = (m.street + ' ' + (m.house_number || '')).trim(); }
        if (m.postcode && postcode) { postcode.value = m.postcode; }
        if (m.city && city) { city.value = m.city; }
        if (m.nvt && nvt) { nvt.value = m.nvt; }
        lookup();
      });
      list.appendChild(btn);
    });
    hint.appendChild(list);
    hint.style.display = 'block';
  }

  // NVT pokriva više adresa u protokolima: klik upiše adresu, NVT i projekt.
  function showProtocolCandidates(list) {
    hint.textContent = list.length + ' Adressen zu diesem NVT — bitte wählen:';
    var box = document.createElement('span');
    box.className = 'lookup-choices';
    list.forEach(function (p) {
      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'lookup-choice';
      var parts = [p.address || '(ohne Adresse)'];
      if (p.project_number) { parts.push('Projekt ' + p.project_number); }
      var meta = [];
      if (p.protocol_type) { meta.push(p.protocol_type); }
      if (p.protocol_date) { meta.push(p.protocol_date); }
      if (meta.length) { parts.push(meta.join(', ')); }
      btn.textContent = parts.join(' · ');
      btn.addEventListener('click', function () {
        if (p.address) { street.value = p.address; }
        if (p.nvt) { nvt.value = p.nvt; }
        // Eksplicitan izbor: GPS sme ponovo da dopuni PLZ/Ort.
        touched['task-postcode'] = false;
        touched['task-city'] = false;
        selectProject(p.project_number);
        // GPS dopunjava PLZ/Ort već u drugom prolazu (protokol ga nosi).
        lookup(true);
      });
      box.appendChild(btn);
    });
    hint.appendChild(box);
    hint.style.display = 'block';
  }

  function lookup(repass) {
    var addr = composedAddress();
    var n = val(nvt);
    if (addr.length < 3 && !n) { return; }
    var q = '?address=' + encodeURIComponent(addr) + '&nvt=' + encodeURIComponent(n);
    fetch('/ha/lookup' + q, { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) {
        if (!d || !d.found) {
          hint.textContent = 'Kein Eintrag im Verzeichnis — Name und NVT bitte manuell eintragen.';
          hint.style.display = 'block';
          return;
        }
        // Više adresa pod istim NVT-om u importovanim protokolima
        // (Fremco/Jetting) — prikaži sve, jer najnoviji pogodak ume da bude
        // pogrešna adresa iz istog NVT-a.
        if (d.source === 'protocol' && d.protocols && d.protocols.length > 1) {
          showProtocolCandidates(d.protocols);
          return;
        }
        // Jedan pogodak iz protokola: dopuni NVT, ulicu i projekt.
        if (d.source === 'protocol' && d.protocol) {
          var pr = d.protocol;
          var filledStreet = fill(street, pr.address);
          setIfEmpty(nvt, pr.nvt);
          selectProject(pr.project_number);
          var meta = [];
          if (pr.protocol_type) { meta.push(pr.protocol_type); }
          if (pr.protocol_date) { meta.push(pr.protocol_date); }
          if (pr.project_number) { meta.push('Projekt ' + pr.project_number); }
          hint.textContent = 'Aus importiertem Protokoll (' + meta.join(', ') + ') übernommen.';
          hint.style.display = 'block';
          verifyWithGps(pr);
          // Drugi prolaz: sa popunjenom adresom probaj i direktorijum.
          if (filledStreet && !repass) { lookup(true); }
          return;
        }
        if (!d.best) {
          hint.textContent = 'Kein Eintrag im Verzeichnis — Name und NVT bitte manuell eintragen.';
          hint.style.display = 'block';
          return;
        }
        var b = d.best;
        if (d.matches && d.matches.length > 1) {
          showCandidates(d.matches);
          return;
        }
        setIfEmpty(nvt, b.nvt);
        if (b.company) {
          setIfEmpty(pick('client_company'), b.company);
        } else {
          setIfEmpty(pick('client_last_name'), b.last_name);
          setIfEmpty(pick('client_first_name'), b.first_name);
        }
        // Direktorijum često ima samo mobilni (Telefon je prazan).
        setIfEmpty(pick('client_phone'), b.phone || b.mobile);
        setIfEmpty(pick('client_mobile'), b.mobile);
        setIfEmpty(pick('client_email'), b.email);
        fill(street, (b.street + ' ' + (b.house_number || '')).trim());
        setIfEmpty(postcode, b.postcode);
        setIfEmpty(city, b.city);
        var parts = [];
        if (b.kunde) { parts.push(b.kunde); }
        if (b.phone) { parts.push('Tel ' + b.phone); }
        if (b.mobile) { parts.push('Mobil ' + b.mobile); }
        if (b.nvt) { parts.push('NVT ' + b.nvt); }
        hint.textContent = 'Directory: ' + parts.join(' · ') + ' — empty fields were filled.';
        hint.style.display = 'block';
      })
      .catch(function () {});
  }

  function schedule() { clearTimeout(timer); timer = setTimeout(function () { lookup(); }, 350); }
  street.addEventListener('blur', schedule);
  street.addEventListener('change', schedule);
  if (postcode) { postcode.addEventListener('blur', schedule); postcode.addEventListener('change', schedule); }
  if (city) { city.addEventListener('blur', schedule); city.addEventListener('change', schedule); }
  if (nvt) {
    nvt.addEventListener('blur', schedule);
    nvt.addEventListener('change', schedule);
  }
  // Dopuna adrese preko Nominatim-a (keširano na serveru):
  // PLZ → grad i ulica+grad → PLZ. Popunjava samo prazna polja.
  var geoTimer = null;
  function fillCityFromPlz() {
    var p = val(postcode);
    if (!/^\d{5}$/.test(p) || val(city)) { return; }
    fetch('/geo/address?plz=' + encodeURIComponent(p), { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d && !d.ambiguous && d.city) { setIfEmpty(city, d.city); } })
      .catch(function () {});
  }
  function fillPlzFromStreet() {
    var s = val(street);
    var c = val(city);
    if (!s || !c || val(postcode)) { return; }
    fetch('/geo/address?street=' + encodeURIComponent(s) + '&city=' + encodeURIComponent(c),
      { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) { if (d && !d.ambiguous && d.postcode) { setIfEmpty(postcode, d.postcode); } })
      .catch(function () {});
  }
  function scheduleGeo() {
    clearTimeout(geoTimer);
    geoTimer = setTimeout(function () { fillCityFromPlz(); fillPlzFromStreet(); }, 450);
  }
  street.addEventListener('blur', scheduleGeo);
  if (postcode) {
    postcode.addEventListener('blur', scheduleGeo);
    postcode.addEventListener('change', scheduleGeo);
  }
  if (city) {
    city.addEventListener('blur', scheduleGeo);
    city.addEventListener('change', scheduleGeo);
  }

  // Izmena zadatka već ima adresu — pokušaj odmah da dopuniš ostalo.
  if (val(street).length >= 3 || val(postcode) || val(city) || val(nvt)) {
    lookup();
  }
})();
