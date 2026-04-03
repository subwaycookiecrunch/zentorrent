document.addEventListener("click", (e) => {
  const a = e.target.closest("a");
  if (a && a.href && a.href.startsWith("magnet:")) {
    e.preventDefault();
    e.stopPropagation();
    chrome.runtime.sendMessage({ magnet: a.href });
  }
}, true);
