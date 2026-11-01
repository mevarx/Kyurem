import { chromium } from "playwright";

const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage();
await page.goto("http://localhost:5173/", { waitUntil: "networkidle" });

// 1. toggle repeatedly
for (let i = 0; i < 4; i++) {
  await page.click(".theme-toggle");
  await page.waitForTimeout(150);
}
const themeAfterToggles = await page.evaluate(() => document.documentElement.getAttribute("data-theme"));
const storedAfterToggles = await page.evaluate(() => localStorage.getItem("kyurem.theme"));

// 2. persist across reload, check for flash: read data-theme at first paint
await page.reload({ waitUntil: "domcontentloaded" });
const early = await page.evaluate(() => {
  const el = document.documentElement;
  return {
    dataTheme: el.getAttribute("data-theme"),
    hasDarkClass: el.classList.contains("dark"),
    bodyBg: getComputedStyle(document.body).backgroundColor,
  };
});
await page.waitForTimeout(300);
const settled = await page.evaluate(() => ({
  dataTheme: document.documentElement.getAttribute("data-theme"),
  hasDarkClass: document.documentElement.classList.contains("dark"),
}));

// 3. no state that only appears in one theme: compare computed text color of sidebar links
async function sample(theme) {
  await page.evaluate((t) => localStorage.setItem("kyurem.theme", t), theme);
  await page.reload({ waitUntil: "networkidle" });
  await page.waitForTimeout(300);
  return page.evaluate(() => {
    const link = document.querySelector('a[href="/tasks"]');
    const icon = link?.querySelector("svg");
    const active = document.querySelector('a[href="/"]');
    const sub = document.querySelector(".brand-text, a[href='/'] span span:last-child");
    const kpi = document.querySelector(".kpi-value");
    return {
      theme: document.documentElement.getAttribute("data-theme"),
      linkColor: link && getComputedStyle(link).color,
      iconColor: icon && getComputedStyle(icon).color,
      activeBg: active && getComputedStyle(active.closest("[data-sidebar='menu-button']") ?? active).backgroundColor,
      activeColor: active && getComputedStyle(active).color,
      subtitle: document.querySelector("a[href='/'] .font-mono")?.textContent,
      subtitleColor: document.querySelector("a[href='/'] .font-mono") && getComputedStyle(document.querySelector("a[href='/'] .font-mono")).color,
      kpiColor: kpi && getComputedStyle(kpi).color,
      kpiFilter: kpi && getComputedStyle(kpi).filter,
      kpiOpacity: kpi && getComputedStyle(kpi).opacity,
      kpiShadow: kpi && getComputedStyle(kpi).textShadow,
    };
  });
}

const dark = await sample("dark");
const light = await sample("light");

// 4. segments
async function segments(theme) {
  await page.evaluate((t) => localStorage.setItem("kyurem.theme", t), theme);
  await page.reload({ waitUntil: "networkidle" });
  await page.waitForTimeout(300);
  return page.evaluate(() => {
    const active = document.querySelector(".time-range-option.is-selected");
    const inactive = [...document.querySelectorAll(".time-range-option")].find((el) => !el.classList.contains("is-selected"));
    const hl = document.querySelector(".seg-highlight");
    return {
      theme: document.documentElement.getAttribute("data-theme"),
      activeColor: getComputedStyle(active).color,
      activeZ: getComputedStyle(active).zIndex,
      highlightBg: hl && getComputedStyle(hl).backgroundColor,
      highlightZ: hl && getComputedStyle(hl).zIndex,
      inactiveColor: getComputedStyle(inactive).color,
      trackBg: getComputedStyle(document.querySelector(".time-range-presets")).backgroundColor,
    };
  });
}

const segDark = await segments("dark");
const segLight = await segments("light");

console.log(JSON.stringify({ themeAfterToggles, storedAfterToggles, early, settled, dark, light, segDark, segLight }, null, 2));
await browser.close();
