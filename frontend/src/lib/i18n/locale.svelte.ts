import {
	baseLocale,
	localStorageKey,
	locales,
	overwriteGetLocale,
	toLocale,
	type Locale,
} from '$lib/paraglide/runtime';

export type { Locale };

/** Все поддерживаемые языки в порядке показа в переключателе. */
export const LOCALES: readonly Locale[] = locales;

/** Самоназвания языков — не переводятся, чтобы свой язык находился в любой локали. */
export const LOCALE_LABELS: Record<Locale, string> = {
	ru: 'Русский',
	en: 'English',
};

function readStoredLocale(): Locale | undefined {
	try {
		return toLocale(localStorage.getItem(localStorageKey)) ?? undefined;
	} catch {
		return undefined; // private mode / запрет хранилища
	}
}

/**
 * Стартовая локаль: явный выбор пользователя → ru (как strategy в
 * scripts/paraglide.mjs). Определяем сами, а не штатным getLocale() рантайма:
 * тот записывает найденную локаль в localStorage, а храним только явный выбор.
 */
function detectLocale(): Locale {
	if (typeof window === 'undefined') return baseLocale;
	return readStoredLocale() ?? baseLocale;
}

let current = $state<Locale>(detectLocale());

// Сообщения Paraglide (m.foo()) читают локаль через getLocale(). Отдаём им
// $state — тогда любой шаблон или $derived, вызвавший m.foo(), подписывается
// на смену языка и перерисовывается без перезагрузки страницы.
overwriteGetLocale(() => current);

/**
 * Локаль Intl для форматов дат и чисел на языке интерфейса. Для английского —
 * en-GB: 24-часовое время и день перед месяцем, как и в русском интерфейсе.
 * Вызов внутри шаблона или $derived подписывается на смену языка.
 */
export function formatLocale(): string {
	return current === 'en' ? 'en-GB' : 'ru-RU';
}

export const locale = {
	get current(): Locale {
		return current;
	},
	/** Ручной выбор языка: применяется сразу и запоминается в этом браузере. */
	set(next: Locale): void {
		if (!LOCALES.includes(next)) return;
		try {
			localStorage.setItem(localStorageKey, next);
		} catch {
			/* quota / private mode: язык сменится до перезагрузки */
		}
		current = next;
	},
};
