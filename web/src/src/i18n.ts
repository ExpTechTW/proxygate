import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import enUS from './locales/en-US.json'
import zhTW from './locales/zh-TW.json'

export const localeConfig = {
  fallback: 'en-US',
  storageKey: 'proxygate:locale',
  resources: {
    'en-US': { label: 'English', translation: enUS },
    'zh-TW': { label: '正體中文', translation: zhTW },
  },
} as const

const detector = new LanguageDetector(null, {
  lookupLocalStorage: localeConfig.storageKey,
})

void i18n
  .use(initReactI18next)
  .use(detector)
  .init({
    resources: localeConfig.resources,
    fallbackLng: localeConfig.fallback,
    supportedLngs: Object.keys(localeConfig.resources),
    detection: {
      caches: ['localStorage'],
      order: ['localStorage', 'navigator'],
    },
    interpolation: { escapeValue: false },
  })

export function resolvedLocale(language?: string): keyof typeof localeConfig.resources {
  return language && language in localeConfig.resources
    ? language as keyof typeof localeConfig.resources
    : localeConfig.fallback
}

export default i18n
