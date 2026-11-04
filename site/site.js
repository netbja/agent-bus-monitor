(function () {
  var fr = document.documentElement.lang === 'fr';
  var label = fr ? 'Copier' : 'Copy';
  var done = fr ? 'Copié' : 'Copied';
  if (!navigator.clipboard) return;
  document.querySelectorAll('.codewrap').forEach(function (w) {
    var pre = w.querySelector('pre');
    var b = document.createElement('button');
    b.type = 'button';
    b.className = 'copy';
    b.textContent = label;
    b.addEventListener('click', function () {
      navigator.clipboard.writeText(pre.innerText).then(function () {
        b.textContent = done;
        setTimeout(function () { b.textContent = label; }, 1500);
      }, function () {});
    });
    w.appendChild(b);
  });
})();
