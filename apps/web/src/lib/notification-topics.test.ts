import { afterEach, describe, expect, it } from 'vitest';
import { setLocale } from '$lib/paraglide/runtime';
import {
	notificationTopicDescription,
	notificationTopicEmailFrequencies,
	notificationTopicGroups,
	notificationTopicLabel,
	type NotificationTopicDefinition
} from './notification-topics';

const backendDefinition: NotificationTopicDefinition = {
	id: 'future_provider_alert',
	group: 'future_group',
	presentation_key: 'notifications.event.future_provider_alert',
	critical_in_app: true,
	transactional: true,
	in_app_mutable: false,
	email_mutable: false,
	mute_applies: false,
	email_frequencies: ['immediate', 'unsupported'],
	default_preference: { in_app: true, email_frequency: 'immediate' }
};

describe('notification topic presentation', () => {
	afterEach(() => setLocale('en', { reload: false }));

	it('keeps backend topic policy while presenting unknown topics safely', () => {
		setLocale('en', { reload: false });
		expect(notificationTopicGroups([backendDefinition])).toEqual([
			{ id: 'future_group', label: 'Notification', topics: [backendDefinition] }
		]);
		expect(notificationTopicEmailFrequencies(backendDefinition)).toEqual(['immediate']);
		expect(notificationTopicLabel(backendDefinition.id)).toBe('Notification');
		expect(notificationTopicDescription(backendDefinition.id)).toBe('Notification');
	});

	it('uses the Portuguese fallback without changing backend policy', () => {
		setLocale('pt', { reload: false });
		expect(notificationTopicGroups([backendDefinition])[0]?.label).toBe('Notificação');
		expect(notificationTopicLabel(backendDefinition.id)).toBe('Notificação');
		expect(notificationTopicEmailFrequencies(backendDefinition)).toEqual(['immediate']);
	});

	it('presents the approval topic with localized copy', () => {
		setLocale('en', { reload: false });
		expect(notificationTopicLabel('workflow_approval')).toBe('Workflow approval');
		expect(notificationTopicDescription('workflow_approval')).not.toBe('Notification');

		setLocale('es', { reload: false });
		expect(notificationTopicLabel('workflow_approval')).toBe('Aprobación de flujo de trabajo');
	});

	it('groups the approval topic under publishing', () => {
		setLocale('en', { reload: false });
		const definition = (id: string): NotificationTopicDefinition => ({
			...backendDefinition,
			id,
			group: 'publishing',
			transactional: false,
			critical_in_app: false,
			in_app_mutable: true,
			email_mutable: true,
			mute_applies: true,
			email_frequencies: ['off', 'immediate', 'daily'],
			default_preference: { in_app: true, email_frequency: 'off' }
		});
		expect(
			notificationTopicGroups([definition('workflow_approval')])
		).toEqual([
			{
				id: 'publishing',
				label: 'Publishing',
				topics: [definition('workflow_approval')]
			}
		]);
	});
});
