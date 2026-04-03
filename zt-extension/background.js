chrome.runtime.onMessage.addListener((request, sender, sendResponse) => {
  if (request.magnet) {
    fetch("http://localhost:9999/stream", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ magnet: request.magnet })
    }).catch(() => {});
  }
});
