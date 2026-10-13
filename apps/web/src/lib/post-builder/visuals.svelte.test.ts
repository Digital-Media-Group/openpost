import { afterAll, afterEach, expect, it, vi } from 'vitest';
import type { ScreenshotDocument } from '@openpost/query-catalog';
import { queryClient } from '$lib/query/client';
import { workspaceCtx } from '$lib/stores/workspace.svelte';
import { renderPostVisual, savePostVisual } from './visuals';

interface HTTPInterception {
	handle?: typeof fetch;
}

// openapi-fetch captures fetch when its client is constructed. Install the HTTP
// boundary before imports so the real client and upload code share this transport.
const http = vi.hoisted(() => {
	const original = globalThis.fetch.bind(globalThis);
	const state: HTTPInterception = {};
	globalThis.fetch = (input, init) => state.handle?.(input, init) ?? original(input, init);
	return { original, state };
});

afterEach(() => {
	http.state.handle = undefined;
	queryClient.clear();
	workspaceCtx.currentWorkspace = null;
	vi.restoreAllMocks();
});
afterAll(() => {
	globalThis.fetch = http.original;
});

it('saves the displayed meme PNG bytes with its editable recipe and export association', async () => {
	workspaceCtx.currentWorkspace = {
		id: 'workspace-1',
		name: 'Studio',
		avatar_url: '',
		color: '#f97316',
		can_edit: true,
		can_connect: true,
		created_at: '2026-10-12T00:00:00Z',
		organization_id: '',
		organization_name: '',
		role: 'admin',
		sso_required: false,
		sso_authenticated: true,
		sso_identity_linked: true
	};
	const document: ScreenshotDocument = {
		schema_version: 1,
		template_id: 'meme',
		title: 'The preference',
		appearance: 'light',
		frame: 'natural',
		text_size: 'normal',
		meme: {
			template_id: 'drake',
			name: 'Drake',
			captions: ['more code', 'more features'],
			overlay_slots: 0,
			overlay_media_ids: [],
			format: 'png'
		}
	};
	const canvas = globalThis.document.createElement('canvas');
	canvas.width = 3;
	canvas.height = 2;
	const context = canvas.getContext('2d');
	if (!context) throw new Error('Canvas2D is unavailable');
	context.fillStyle = '#d32f2f';
	context.fillRect(0, 0, 3, 2);
	const displayedPNG = canvas.toDataURL('image/png');
	const requests: Array<{ path: string; body: unknown }> = [];
	http.state.handle = async (input, init) => {
		const request = input instanceof Request ? input : new Request(input, init);
		const path = new URL(request.url).pathname;
		if (!path.startsWith('/api/v1/')) return http.original(input, init);
		requests.push({ path, body: request.method === 'GET' ? undefined : await request.json() });
		switch (path) {
			case '/api/v1/memes/preview':
				return Response.json({ mime_type: 'image/png', data_base64: displayedPNG.split(',')[1] });
			case '/api/v1/screenshot-templates/designs':
				return Response.json({
					id: 'design-1',
					revision: 1,
					workspace_id: 'workspace-1',
					document
				});
			case '/api/v1/media/storage':
				return Response.json({
					direct_upload_supported: true,
					asset_count: 0,
					internal_bytes: 0,
					limit_bytes: 0,
					used_bytes: 0
				});
			case '/api/v1/media/upload-session':
				return Response.json({
					media_id: 'media-1',
					deduped: false,
					complete_url: '/api/v1/media/upload-session/media-1/complete',
					upload: {
						method: 'PUT',
						url: '/api/v1/media/upload-session/media-1/content',
						headers: {},
						expires_at: '2026-10-12T23:00:00Z',
						object_key: 'media-1.png'
					}
				});
			case '/api/v1/media/upload-session/media-1/complete':
				return Response.json({ id: 'media-1', mime_type: 'image/png', url: '/media/media-1' });
			case '/api/v1/screenshot-templates/designs/design-1/exports':
				return Response.json({});
			default:
				throw new Error('Unexpected HTTP request: ' + request.method + ' ' + path);
		}
	};
	let uploaded: Blob | undefined;
	const open = vi.spyOn(XMLHttpRequest.prototype, 'open');
	vi.spyOn(XMLHttpRequest.prototype, 'send').mockImplementation(
		function (this: XMLHttpRequest, body) {
			if (!(body instanceof Blob)) throw new Error('Expected PNG upload bytes');
			uploaded = body;
			Object.defineProperty(this, 'status', { value: 200 });
			this.dispatchEvent(new Event('load'));
		}
	);
	const visual = await renderPostVisual('workspace-1', document);
	expect(visual.url).toBe(displayedPNG);
	expect(visual.aspectRatio).toBe(1.5);
	const media = await savePostVisual('workspace-1', visual, 'Prefer fewer lines.');
	expect(media.id).toBe('media-1');
	expect(requests.map((request) => request.path)).toEqual([
		'/api/v1/memes/preview',
		'/api/v1/screenshot-templates/designs',
		'/api/v1/media/storage',
		'/api/v1/media/upload-session',
		'/api/v1/media/upload-session/media-1/complete',
		'/api/v1/screenshot-templates/designs/design-1/exports'
	]);
	expect(requests[1].body).toEqual({ workspace_id: 'workspace-1', document });
	expect(requests[3].body).toMatchObject({
		workspace_id: 'workspace-1',
		filename: 'The preference.png',
		mime_type: 'image/png',
		source: 'screenshot_template',
		alt_text: 'Prefer fewer lines.'
	});
	expect(open.mock.calls[0].slice(0, 2)).toEqual([
		'PUT',
		'/api/v1/media/upload-session/media-1/content'
	]);
	expect(uploaded).toBeInstanceOf(File);
	if (!uploaded) throw new Error('The preview PNG was not uploaded');
	expect(uploaded.type).toBe('image/png');
	expect(new Uint8Array(await uploaded.arrayBuffer())).toEqual(
		new Uint8Array(await (await http.original(displayedPNG)).arrayBuffer())
	);
	expect(requests[5].body).toEqual({ revision: 1, media_id: 'media-1' });
});
