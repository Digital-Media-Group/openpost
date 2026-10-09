import type { ManifestOptions } from 'vite-plugin-pwa';

export const appManifest = {
	id: '/',
	scope: '/',
	name: 'Gestión de RRSS',
	short_name: 'Gestión de RRSS',
	description: 'Programa y publica contenido en tus redes sociales.',
	theme_color: '#b74c05',
	background_color: '#faf9f7',
	display: 'standalone',
	start_url: '/',
	icons: [
		{
			src: '/assets/brand/pwa-192.png',
			sizes: '192x192',
			type: 'image/png',
			purpose: 'any'
		},
		{
			src: '/assets/brand/pwa-512.png',
			sizes: '512x512',
			type: 'image/png',
			purpose: 'any'
		},
		{
			src: '/assets/brand/pwa-maskable-512.png',
			sizes: '512x512',
			type: 'image/png',
			purpose: 'maskable'
		},
		{
			src: '/assets/brand/icon.svg',
			sizes: 'any',
			type: 'image/svg+xml',
			purpose: 'any'
		}
	]
} satisfies Partial<ManifestOptions>;
