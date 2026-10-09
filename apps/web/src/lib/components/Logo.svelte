<script lang="ts">
	interface Props {
		class?: string;
		width?: number;
		height?: number;
		showText?: boolean;
		decorative?: boolean;
	}

	let {
		class: className = '',
		width = 100,
		height = 28,
		showText = false,
		decorative = false
	}: Props = $props();
	const size = $derived(Math.min(width, height));
</script>

<div class="flex items-center gap-2 {className}">
	<svg
		xmlns="http://www.w3.org/2000/svg"
		viewBox="0 0 128 128"
		width={size}
		height={size}
		role={decorative || showText ? undefined : 'img'}
		aria-label={decorative || showText ? undefined : 'Gestión de RRSS'}
		aria-hidden={decorative || showText ? 'true' : undefined}
		class="h-auto max-w-full shrink-0"
	>
		<!-- Un nodo que publica, dos redes que reciben: publicar una vez,
		     repartir a todas. Misma caja de 128 que antes, así que encaja en
		     los mismos huecos sin tocar los tamaños de quien lo usa. -->
		<g fill="none" stroke="var(--action-focal)" stroke-width="13" stroke-linecap="round">
			<path d="M64 35 28 99" />
			<path d="M64 35 100 99" />
			<path d="M28 99h72" />
		</g>
		<g fill="var(--action-focal)">
			<circle cx="64" cy="35" r="22" />
			<circle cx="28" cy="99" r="16" />
			<circle cx="100" cy="99" r="16" />
		</g>
	</svg>
	{#if showText}
		<span
			class="font-brand leading-none font-semibold tracking-[-0.02em] whitespace-nowrap text-sidebar-foreground"
			>Gestión de RRSS</span
		>
	{/if}
</div>
