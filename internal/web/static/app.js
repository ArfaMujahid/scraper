// Dashboard client: start a scrape, stream its live stats over SSE, and keep the
// job list fresh. No framework — just fetch + EventSource + DOM updates.

const $ = (id) => document.getElementById(id);
let currentStream = null;

// startScrape posts the seeds (and optional max-pages) and returns the job id.
async function startScrape(seeds, maxPages) {
  const res = await fetch("/api/scrape", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ seeds, max_pages: maxPages || 0 }),
  });
  if (!res.ok) {
    throw new Error((await res.text()) || res.statusText);
  }
  const { job } = await res.json();
  return job;
}

// streamJob opens an SSE connection and updates the live metrics until the job
// finishes, then refreshes the job list.
function streamJob(id) {
  if (currentStream) currentStream.close();
  const es = new EventSource("/api/events?job=" + encodeURIComponent(id));
  currentStream = es;

  es.onmessage = (e) => {
    const f = JSON.parse(e.data);
    setStatus(f.status);
    $("m-done").textContent = f.done;
    $("m-inflight").textContent = f.in_flight;
    $("m-errors").textContent = f.errors;
    $("m-rate").textContent = (f.pages_per_sec || 0).toFixed(1);
    $("m-bytes").textContent = Math.round((f.bytes || 0) / 1024);
    $("m-elapsed").textContent = ((f.elapsed_ms || 0) / 1000).toFixed(1);
    renderFeed(f.results || []);

    if (f.status === "done" || f.status === "failed") {
      es.close();
      currentStream = null;
      showDownloads(id);
      refreshJobs();
    }
  };
  es.onerror = () => { es.close(); currentStream = null; };
}

// showDownloads points the JSONL/CSV download links at the finished job.
function showDownloads(id) {
  const box = $("downloads");
  box.querySelectorAll("a.download").forEach((a) => {
    a.href = "/api/download?job=" + encodeURIComponent(id) + "&format=" + a.dataset.fmt;
  });
  box.hidden = false;
}

// renderFeed paints the most-recent-first list of scraped pages.
function renderFeed(items) {
  const feed = $("feed");
  $("feed-count").textContent = items.length ? `(${items.length})` : "";
  if (!items.length) {
    feed.innerHTML = '<li class="empty">Nothing scraped yet.</li>';
    return;
  }
  feed.innerHTML = "";
  for (const it of items) {
    const li = document.createElement("li");
    const ok = !it.error && it.status >= 200 && it.status < 400;
    const mark = ok ? "ok" : "err";
    const label = it.error ? it.error : (it.title || "(no title)");
    li.className = "feed-item " + mark;
    li.innerHTML =
      `<span class="code ${mark}">${it.status || "ERR"}</span>` +
      `<span class="ftitle">${escapeHtml(label)}</span>` +
      `<span class="furl">${escapeHtml(it.url)}</span>`;
    feed.appendChild(li);
  }
}

// setStatus updates the status pill.
function setStatus(status) {
  const el = $("live-status");
  el.textContent = status;
  el.className = "status " + (["running", "done", "failed"].includes(status) ? status : "idle");
}

// refreshJobs re-renders the owner's job list.
async function refreshJobs() {
  const res = await fetch("/api/jobs");
  if (!res.ok) return;
  const jobs = await res.json();
  const list = $("job-list");
  list.innerHTML = "";
  if (!jobs.length) {
    list.innerHTML = '<li class="empty">No jobs yet.</li>';
    return;
  }
  for (const j of jobs) {
    const li = document.createElement("li");
    const seed = (j.seeds && j.seeds[0]) || j.id;
    const more = j.seeds && j.seeds.length > 1 ? ` (+${j.seeds.length - 1})` : "";
    li.innerHTML =
      `<span class="seed" title="${j.id}">${escapeHtml(seed)}${more}</span>` +
      `<span class="badge ${j.status}">${j.status}</span>`;
    list.appendChild(li);
  }
}

// escapeHtml prevents scraped/seed text from injecting markup.
function escapeHtml(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}

document.addEventListener("DOMContentLoaded", () => {
  refreshJobs();
  $("scrape-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const seeds = $("seeds").value.split("\n").map((s) => s.trim()).filter(Boolean);
    const maxPages = parseInt($("max-pages").value, 10) || 0;
    const msg = $("form-msg");
    if (!seeds.length) { msg.textContent = "Enter at least one URL."; return; }

    const btn = $("start-btn");
    btn.disabled = true;
    msg.textContent = "Starting…";
    try {
      renderFeed([]);
      $("downloads").hidden = true;
      ["m-done", "m-inflight", "m-errors"].forEach((k) => ($(k).textContent = "0"));
      const id = await startScrape(seeds, maxPages);
      msg.textContent = "Started.";
      streamJob(id);
      refreshJobs();
    } catch (err) {
      msg.textContent = "Error: " + err.message;
    } finally {
      btn.disabled = false;
    }
  });
});
