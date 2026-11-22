// The language that is showing right now, as reactive state. t() and the Intl helpers read
// `version`, so anything that formats text inside a $derived or a template updates when the
// language changes; App.svelte also keys its whole tree on it, which catches text that was
// formatted once at start-up.
class LocaleState {
  /** The catalog code in use: "en", "de", "pt-BR". */
  code = $state('en');
  /** Bumped on every language change. */
  version = $state(0);
}

export const localeState = new LocaleState();
