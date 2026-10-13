<script lang="ts">
	import { client } from '$lib/api/client';
	import { loadWorkspaceAccounts } from '$lib/api/performance-cache';
	import type { SocialAccount } from '$lib/api/client';
	import { ThemeIcon } from '$lib/themes/icons';
	import { workspaceCtx } from '$lib/stores/workspace.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import PageContainer from '$lib/components/page-container.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import InlineNotice from '$lib/components/inline-notice.svelte';
	import AppToast from '$lib/components/app-toast.svelte';
	import PlatformIcon from '$lib/components/platform-icon.svelte';
	import { m } from '$lib/paraglide/messages';
	import { formatSocialAccountName } from '$lib/utils';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';

	type PlanSlot = {
		index: number;
		scheduled_at: string;
		topic: string;
		angle: string;
		text: string;
	};

	const workspaceID = $derived(workspaceCtx.currentWorkspace?.id ?? '');
	const viewerTimeZone = $derived(
		workspaceCtx.settingsWorkspaceID === workspaceID
			? workspaceCtx.settings.timezone || 'UTC'
			: 'UTC'
	);

	let brief = $state('');
	let days = $state(7);
	let slots = $state(5);
	let hour = $state(9);
	let startDate = $state('');
	let startDateTouched = $state(false);
	let accountIDs = $state<string[]>([]);
	let accounts = $state.raw<SocialAccount[]>([]);
	let accountsLoading = $state(false);
	let accountsError = $state('');

	// Planning and landing are separate requests on purpose: planning writes
	// nothing, so a plan can be read, disliked, and replaced without leaving
	// drafts behind. Only landing creates anything.
	let plan = $state.raw<PlanSlot[] | null>(null);
	let planModel = $state('');
	let planning = $state(false);
	let planError = $state('');
	let landing = $state(false);
	let toastMessage = $state('');
	let toastTone = $state<'success' | 'error'>('success');
	let idempotencyKey = $state('');

	const canPlan = $derived(
		Boolean(workspaceID) &&
			brief.trim().length > 0 &&
			accountIDs.length > 0 &&
			startDate.length > 0 &&
			slots >= 1 &&
			slots <= days &&
			!planning
	);

	function todayInViewerZone(timeZone: string): string {
		return new Intl.DateTimeFormat('en-CA', {
			timeZone,
			year: 'numeric',
			month: '2-digit',
			day: '2-digit'
		}).format(new Date());
	}

	// The Workspace time zone is only known after settings load, so the default
	// day is filled in once it is, and never overwritten afterwards: an author
	// who picked a day keeps it.
	$effect(() => {
		if (startDateTouched) return;
		const today = todayInViewerZone(viewerTimeZone);
		if (startDate !== today) startDate = today;
	});

	$effect(() => {
		if (!workspaceID) return;
		let cancelled = false;
		accountsLoading = true;
		accountsError = '';
		loadWorkspaceAccounts(workspaceID)
			.then((loaded) => {
				if (cancelled) return;
				accounts = loaded;
				// Default to every account the workspace can publish to, which is
				// the common case: one plan, all destinations.
				accountIDs = loaded.map((account) => account.id);
			})
			.catch(() => {
				if (cancelled) return;
				accountsError = m.planner_accounts_failed();
			})
			.finally(() => {
				if (cancelled) return;
				accountsLoading = false;
			});
		return () => {
			cancelled = true;
		};
	});

	// The days are deliberately not previewed here. The server owns the
	// schedule, and a second implementation of the same arithmetic in the client
	// would be a second source of truth for the calendar — the exact thing the
	// server-side schedule exists to prevent. The plan comes back with the real
	// dates on it, which is what the author reviews.
	function formatSlotDate(value: string): string {
		const date = new Date(value);
		if (Number.isNaN(date.getTime())) return value;
		return new Intl.DateTimeFormat(undefined, {
			timeZone: viewerTimeZone,
			weekday: 'short',
			day: 'numeric',
			month: 'short',
			hour: '2-digit',
			minute: '2-digit'
		}).format(date);
	}

	async function planWeek() {
		if (!canPlan) return;
		planning = true;
		planError = '';
		try {
			const { data, error } = await client.POST('/publication-plans', {
				body: {
					workspace_id: workspaceID,
					brief: brief.trim(),
					start_date: startDate,
					days,
					slots,
					hour,
					time_zone: viewerTimeZone,
					social_account_ids: accountIDs
				}
			});
			if (error || !data) {
				planError = m.planner_failed();
				plan = null;
				return;
			}
			plan = data.slots ?? [];
			planModel = data.model ?? '';
		} catch {
			planError = m.planner_failed();
			plan = null;
		} finally {
			planning = false;
		}
	}

	async function landPlan() {
		if (!plan || plan.length === 0 || landing) return;
		landing = true;
		// A fresh key per attempt, so a deliberate second landing of an edited
		// plan is not mistaken for a replay of the first.
		idempotencyKey = crypto.randomUUID();
		try {
			const { data, error } = await client.POST('/publication-plans/land', {
				params: { header: { 'Idempotency-Key': idempotencyKey } },
				body: {
					workspace_id: workspaceID,
					brief: brief.trim(),
					start_date: startDate,
					days,
					hour,
					time_zone: viewerTimeZone,
					social_account_ids: accountIDs,
					plan
				}
			});
			const created = data?.publications ?? [];
			if (error) {
				toastTone = 'error';
				toastMessage = m.planner_land_failed();
				return;
			}
			// A partial landing answers 200 with the drafts that were created and
			// a problem note, so the drafts are never hidden behind an error.
			if (created.length === 0) {
				toastTone = 'error';
				toastMessage = m.planner_land_failed();
				return;
			}
			if (data?.problem) {
				toastTone = 'error';
				toastMessage = m.planner_landed_partial({ count: created.length });
				return;
			}
			toastTone = 'success';
			toastMessage = m.planner_landed({ count: created.length });
			plan = null;
			await goto(resolve('/calendar'));
		} catch {
			toastTone = 'error';
			toastMessage = m.planner_land_failed();
		} finally {
			landing = false;
		}
	}

	function toggleAccount(id: string) {
		accountIDs = accountIDs.includes(id)
			? accountIDs.filter((value) => value !== id)
			: [...accountIDs, id];
	}
</script>

<svelte:head>
	<title>{m.planner_title()}</title>
</svelte:head>

{#if toastMessage}
	<AppToast
		message={toastMessage}
		tone={toastTone}
		dismissLabel={m.common_close()}
		onDismiss={() => (toastMessage = '')}
	/>
{/if}

<PageContainer
	title={m.planner_title()}
	description={m.planner_description()}
	themeIconRole="idea"
	loading={accountsLoading}
	loadingMessage={m.common_loading()}
>
	{#snippet actions()}
		<Button onclick={planWeek} disabled={!canPlan} aria-busy={planning} class="gap-2">
			<ThemeIcon role="sparkles" class="size-4" />
			{plan ? m.planner_replan() : m.planner_plan()}
		</Button>
	{/snippet}

	<div class="space-y-6">
		{#if accountsError}
			<InlineNotice tone="error" message={accountsError} />
		{/if}

		{#if planError}
			<InlineNotice tone="error" message={planError} />
		{/if}

		<div class="grid gap-6 lg:grid-cols-2">
			<div class="space-y-4">
				<div class="space-y-2">
					<Label for="planner-brief">{m.planner_brief_label()}</Label>
					<Textarea
						id="planner-brief"
						bind:value={brief}
						rows={5}
						maxlength={2000}
						placeholder={m.planner_brief_placeholder()}
					/>
					<p class="text-sm text-muted-foreground">{m.planner_brief_hint()}</p>
				</div>

				<div class="grid gap-4 sm:grid-cols-2">
					<div class="space-y-2">
						<Label for="planner-start">{m.planner_start_label()}</Label>
						<Input
							id="planner-start"
							type="date"
							bind:value={startDate}
							oninput={() => (startDateTouched = true)}
						/>
					</div>
					<div class="space-y-2">
						<Label for="planner-hour">{m.planner_hour_label()}</Label>
						<Input id="planner-hour" type="number" min={0} max={23} bind:value={hour} />
					</div>
					<div class="space-y-2">
						<Label for="planner-days">{m.planner_days_label()}</Label>
						<Input id="planner-days" type="number" min={1} max={31} bind:value={days} />
					</div>
					<div class="space-y-2">
						<Label for="planner-slots">{m.planner_slots_label()}</Label>
						<Input id="planner-slots" type="number" min={1} max={14} bind:value={slots} />
					</div>
				</div>

				{#if slots > days}
					<InlineNotice tone="error" message={m.planner_slots_exceed_days()} />
				{/if}

				<div class="space-y-2">
					<Label>{m.planner_destinations_label()}</Label>
					{#if accounts.length === 0 && !accountsLoading}
						<p class="text-sm text-muted-foreground">{m.planner_no_accounts()}</p>
					{:else}
						<div class="flex flex-wrap gap-2">
							{#each accounts as account (account.id)}
								<Button
									variant={accountIDs.includes(account.id) ? 'default' : 'outline'}
									size="sm"
									class="gap-2"
									onclick={() => toggleAccount(account.id)}
								>
									<PlatformIcon platform={account.platform} class="size-4" />
									{formatSocialAccountName(
										account.account_username,
										account.platform,
										account.alias ?? ''
									) || account.platform}
								</Button>
							{/each}
						</div>
					{/if}
				</div>

				{#if slots >= 1 && slots <= days && startDate}
					<p class="text-sm text-muted-foreground">
						{m.planner_preview_summary({ count: slots, days, hour })}
					</p>
				{/if}
			</div>

			<div class="space-y-4">
				{#if !plan}
					<EmptyState
						themeIconRole="idea"
						title={m.planner_empty_title()}
						description={m.planner_empty_description()}
					/>
				{:else}
					<div class="flex items-center justify-between gap-4">
						<p class="text-sm text-muted-foreground">
							{m.planner_slot_count({ count: plan.length })}
						</p>
						<Button onclick={landPlan} disabled={landing} aria-busy={landing}>
							{landing ? m.planner_landing() : m.planner_land()}
						</Button>
					</div>

					<ul class="space-y-3">
						{#each plan as slot (slot.index)}
							<li class="rounded-lg border p-4">
								<div class="flex items-baseline justify-between gap-3">
									<span class="font-medium">{slot.topic}</span>
									<span class="shrink-0 text-xs text-muted-foreground">
										{formatSlotDate(slot.scheduled_at)}
									</span>
								</div>
								{#if slot.angle}
									<p class="mt-1 text-sm text-muted-foreground">{slot.angle}</p>
								{/if}
								{#if slot.text}
									<p class="mt-2 text-sm whitespace-pre-wrap">{slot.text}</p>
								{/if}
							</li>
						{/each}
					</ul>

					{#if planModel}
						<p class="text-xs text-muted-foreground">{m.planner_model({ model: planModel })}</p>
					{/if}
				{/if}
			</div>
		</div>
	</div>
</PageContainer>
