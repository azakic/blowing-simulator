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

  function setIfEmpty(el, value) {
    if (el && value && !el.value.trim()) { el.value = value; }
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

  function lookup() {
    var addr = composedAddress();
    var n = val(nvt);
    if (addr.length < 3 && !n) { return; }
    var q = '?address=' + encodeURIComponent(addr) + '&nvt=' + encodeURIComponent(n);
    fetch('/ha/lookup' + q, { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) {
        if (!d || !d.found || !d.best) {
          hint.textContent = 'Kein Eintrag im Verzeichnis — Name und NVT bitte manuell eintragen.';
          hint.style.display = 'block';
          return;
        }
        var b = d.best;
        if (d.matches && d.matches.length > 1) {
          var names = d.matches.map(function (m) { return m.address; });
          hint.textContent = 'NVT ' + (b.nvt || '') + ' covers ' + d.matches.length +
            ' addresses: ' + names.join(' · ') + ' — type the address for an exact match.';
          hint.style.display = 'block';
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
        if (!val(street) && (b.street || b.house_number)) {
          street.value = (b.street + ' ' + (b.house_number || '')).trim();
        }
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

  function schedule() { clearTimeout(timer); timer = setTimeout(lookup, 350); }
  street.addEventListener('blur', schedule);
  street.addEventListener('change', schedule);
  if (postcode) { postcode.addEventListener('blur', schedule); postcode.addEventListener('change', schedule); }
  if (city) { city.addEventListener('blur', schedule); city.addEventListener('change', schedule); }
  if (nvt) {
    nvt.addEventListener('blur', schedule);
    nvt.addEventListener('change', schedule);
  }
  // Izmena zadatka već ima adresu — pokušaj odmah da dopuniš ostalo.
  if (val(street).length >= 3 || val(postcode) || val(city) || val(nvt)) {
    lookup();
  }
})();
