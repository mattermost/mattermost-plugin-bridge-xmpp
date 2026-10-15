// Kill switch for the service worker that the previous xmpp-web client registered on
// this origin. A registered worker keeps serving its cached app shell even after the
// container behind the port is replaced, so it has to unregister itself once.
// Safe to keep: browsers that never saw xmpp-web simply never request this file.
self.addEventListener('install', () => self.skipWaiting());

self.addEventListener('activate', (event) => {
    event.waitUntil((async () => {
        const keys = await caches.keys();
        await Promise.all(keys.map((key) => caches.delete(key)));
        await self.registration.unregister();
        const clients = await self.clients.matchAll({ type: 'window' });
        clients.forEach((client) => client.navigate(client.url));
    })());
});
