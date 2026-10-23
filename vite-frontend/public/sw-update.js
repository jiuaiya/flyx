// Upgrade already-open tabs too, including clients running the old prompt-only
// registration code. Run after precaching finishes, so their new HTML and chunks
// belong to the same build. A first installation does not reload the page.
const flyxReplacingWorker = Boolean(self.registration.active);

self.addEventListener("activate", (event) => {
  if (!flyxReplacingWorker) return;

  event.waitUntil(
    self.clients.claim().then(async () => {
      const tabs = await self.clients.matchAll({ type: "window" });

      tabs.forEach((tab) => {
        // OAuth callback codes are single-use; let an in-flight login finish.
        if (new URL(tab.url).pathname.endsWith("/github-callback")) return;

        // Do not wait for navigation inside activate: its fetches may need
        // activation to finish before the new worker can serve them.
        void tab.navigate(tab.url).catch(() => {});
      });
    }),
  );
});
