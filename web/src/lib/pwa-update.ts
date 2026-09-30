export async function updateServiceWorker(registration?: ServiceWorkerRegistration): Promise<void> {
  if (registration) {
    await registration.update()
    return
  }

  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) {
    throw new Error('Service Worker is not supported by this browser')
  }

  const currentRegistration = await navigator.serviceWorker.getRegistration()
  if (!currentRegistration) {
    throw new Error('No Service Worker registration was found')
  }

  await currentRegistration.update()
}
