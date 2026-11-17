#!/usr/bin/env node
// shots.mjs — mengambil tangkapan layar tiap layar UI untuk dokumentasi.
//
// Memakai puppeteer-core dengan Chromium yang sudah terpasang, jadi tidak
// perlu mengunduh browser. Jalankan di dalam container yang punya modul
// puppeteer-core dan Chromium.
//
//   node shots.mjs <base-url> <folder-tujuan>
//
// Contoh: node shots.mjs http://172.17.0.4:8080 /tmp/shots
import puppeteer from "puppeteer-core";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const BASE = process.argv[2] || "http://127.0.0.1:8080";
const OUT = process.argv[3] || "/tmp/shots";

const ADMIN_USER = process.env.TS_ADMIN_USERNAME || "admin";
const ADMIN_PASS = process.env.TS_ADMIN_PASSWORD || "admin-password-123";
const DEMO_USER = process.env.TS_DEMO_USERNAME || "mahasiswa01";
const DEMO_PASS = process.env.TS_DEMO_PASSWORD || "mahasiswa01-secret";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  mkdirSync(OUT, { recursive: true });

  const browser = await puppeteer.launch({
    executablePath: process.env.CHROME_PATH || "/usr/lib/chromium/chromium",
    args: ["--no-sandbox", "--disable-dev-shm-usage", "--single-process", "--no-zygote"],
  });

  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });

  const shot = async (name, full = false) => {
    await page.screenshot({ path: join(OUT, `${name}.png`), fullPage: full });
    console.log("tangkapan layar:", name);
  };

  const login = async (user, pass) => {
    await page.goto(`${BASE}/login`, { waitUntil: "networkidle0" });
    await page.evaluate(
      (u, p) => {
        document.querySelector("input[name=username]").value = u;
        document.querySelector("input[name=password]").value = p;
        document.querySelector("button[type=submit]").click();
      },
      user,
      pass
    );
    await sleep(1500);
  };

  const state = (fn) => page.evaluate(fn);

  await page.goto(`${BASE}/login`, { waitUntil: "networkidle0" });
  await shot("01-login");

  await login(DEMO_USER, DEMO_PASS);
  await shot("02-files-list");

  await state(() => window.Alpine.$data(document.body).s.toggleView());
  await sleep(400);
  await shot("03-files-grid");
  await state(() => window.Alpine.$data(document.body).s.toggleView());
  await sleep(300);

  await state(async () => {
    const input = document.querySelector("input[type=file]");
    const dt = new DataTransfer();
    dt.items.add(new File(["%PDF-1.4 soal ujian tengah semester"], "UTS-01-soal.pdf", { type: "application/pdf" }));
    dt.items.add(new File(["%PDF-1.4 kunci jawaban"], "UTS-01-jawaban.pdf", { type: "application/pdf" }));
    input.files = dt.files;
    input.dispatchEvent(new Event("change", { bubbles: true }));
    window.Alpine.$data(document.body).s.openUpload();
  });
  await sleep(500);
  await shot("04-upload-modal");

  await state(() => {
    const s = window.Alpine.$data(document.body).s;
    s.uploadOpen = false;
    s.startRename(s.filtered[0]);
  });
  await sleep(400);
  await shot("05-rename-dialog");

  await state(() => {
    const s = window.Alpine.$data(document.body).s;
    s.renameOpen = false;
    s.askDelete(s.filtered[0]);
  });
  await sleep(400);
  await shot("06-delete-dialog");

  await page.evaluate(() => document.querySelector("form[action='/logout']").submit());
  await sleep(1200);
  await login(ADMIN_USER, ADMIN_PASS);
  await page.goto(`${BASE}/admin`, { waitUntil: "networkidle0" });
  await sleep(1200);
  await shot("07-admin", true);

  await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: "dark" }]);
  await page.goto(`${BASE}/login`, { waitUntil: "networkidle0" });
  await sleep(400);
  await shot("08-dark-mode");

  await browser.close();
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
