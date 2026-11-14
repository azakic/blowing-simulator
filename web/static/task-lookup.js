// Prediktivno popunjavanje zadatka iz direktorijuma klijenata (/admin/import).
// Kada se unese adresa (ili NVT), prazna polja se popunjavaju: NVT i naslov
// (ime i prezime klijenta). Postojeće vrednosti se nikad ne prepisuju.
(function () {
  var address = document.getElementById('task-address');
  var nvt = document.getElementById('task-nvt');
  var hint = document.getElementById('task-lookup-hint');
  if (!address || !hint) { return; }
  var timer = null;

  function setIfEmpty(el, value) {
    if (el && value && !el.value.trim()) { el.value = value; }
  }

  function lookup() {
    var addr = (address.value || '').trim();
    var n = nvt ? (nvt.value || '').trim() : '';
    if (addr.length < 3 && !n) { return; }
    var q = '?address=' + encodeURIComponent(addr) + '&nvt=' + encodeURIComponent(n);
    fetch('/ha/lookup' + q, { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (d) {
        if (!d || !d.found || !d.best) { hint.style.display = 'none'; return; }
        var b = d.best;
        if (d.matches && d.matches.length > 1) {
          var names = d.matches.map(function (m) { return m.address; });
          hint.textContent = 'NVT ' + (b.nvt || '') + ' covers ' + d.matches.length +
            ' addresses: ' + names.join(' · ') + ' — type the address for an exact match.';
          hint.style.display = 'block';
          return;
        }
        setIfEmpty(nvt, b.nvt);
        setIfEmpty(document.querySelector('input[name="client_last_name"]'), b.last_name);
        setIfEmpty(document.querySelector('input[name="client_first_name"]'), b.first_name);
        // Direktorijum često ima samo mobilni (Telefon je prazan).
        setIfEmpty(document.querySelector('input[name="client_phone"]'), b.phone || b.mobile);
        var parts = [];
        if (b.kunde) { parts.push(b.kunde); }
        if (b.phone) { parts.push('Tel ' + b.phone); }
        if (b.mobile) { parts.push('Mobil ' + b.mobile); }
        if (b.nvt) { parts.push('NVT ' + b.nvt); }
        hint.textContent = 'Directory: ' + parts.join(' · ') + ' — NVT/name filled when empty.';
        hint.style.display = 'block';
      })
      .catch(function () {});
  }

  function schedule() { clearTimeout(timer); timer = setTimeout(lookup, 350); }
  address.addEventListener('blur', schedule);
  address.addEventListener('change', schedule);
  if (nvt) {
    nvt.addEventListener('blur', schedule);
    nvt.addEventListener('change', schedule);
  }
  // Izmena zadatka već ima adresu — pokušaj odmah da dopuniš NVT.
  if ((address.value || '').trim().length >= 3 || (nvt && nvt.value.trim())) {
    lookup();
  }
})();
