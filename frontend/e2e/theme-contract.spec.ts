import { expect, test, type Locator, type Page } from '@playwright/test';

type Theme = 'dark' | 'light';

type ThemeTokens = {
  bg: string;
  surface: string;
  surfaceStrong: string;
  text: string;
  secondary: string;
  subtle: string;
  accent: string;
  accentStrong: string;
  accentSoft: string;
  success: string;
  successSoft: string;
  error: string;
  errorSoft: string;
};

type ThemeProbe = {
  initialTheme: string | null;
  initialStorageKeys: string[];
  observedThemes: Array<string | null>;
  domContentLoadedTheme: string | null;
};

type ThemeControl =
  | {
      kind: 'choices';
      dark: Locator;
      light: Locator;
    }
  | {
      kind: 'toggle';
      toggle: Locator;
    };

type RGB = [number, number, number];

const themeStorageKey = 'task-per-minute-theme';
const viewportWidths = [375, 768, 1440] as const;

const expectedTokens: Record<Theme, ThemeTokens> = {
  dark: {
    bg: '#121212',
    surface: '#1B1B1B',
    surfaceStrong: '#242424',
    text: '#F2F2F2',
    secondary: '#B5B5B5',
    subtle: '#939393',
    accent: '#D0D0D0',
    accentStrong: '#F5F5F5',
    accentSoft: '#2D2D2D',
    success: '#68B381',
    successSoft: '#1D3025',
    error: '#E07171',
    errorSoft: '#3A2024',
  },
  light: {
    bg: '#F5F5F5',
    surface: '#FFFFFF',
    surfaceStrong: '#E9E9E9',
    text: '#202020',
    secondary: '#5D5D5D',
    subtle: '#626262',
    accent: '#444444',
    accentStrong: '#1F1F1F',
    accentSoft: '#E8E8E8',
    success: '#286B40',
    successSoft: '#E7F3EB',
    error: '#B4232D',
    errorSoft: '#FCEDEE',
  },
};

const installThemeProbe = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    const probeAttribute = 'data-theme-contract-probe';
    let installed = false;

    const install = (): boolean => {
      const root = document.documentElement;
      if (!root || installed) {
        return installed;
      }
      installed = true;

      let initialStorageKeys: string[] = [];
      try {
        initialStorageKeys = Object.keys(window.localStorage);
      } catch {
        // The storage-failure contract is covered by a separate test.
      }
      const probe: ThemeProbe = {
        initialTheme: root.getAttribute('data-theme'),
        initialStorageKeys,
        observedThemes: [root.getAttribute('data-theme')],
        domContentLoadedTheme: null,
      };

      root.setAttribute(probeAttribute, JSON.stringify(probe));

      const observer = new MutationObserver(() => {
        probe.observedThemes.push(root.getAttribute('data-theme'));
        root.setAttribute(probeAttribute, JSON.stringify(probe));
      });
      observer.observe(root, { attributes: true, attributeFilter: ['data-theme'] });

      const recordDOMContentLoaded = () => {
        probe.domContentLoadedTheme = root.getAttribute('data-theme');
        root.setAttribute(probeAttribute, JSON.stringify(probe));
        observer.disconnect();
      };
      if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', recordDOMContentLoaded, { once: true });
      } else {
        recordDOMContentLoaded();
      }

      return true;
    };

    if (!install()) {
      const documentObserver = new MutationObserver(() => {
        if (install()) {
          documentObserver.disconnect();
        }
      });
      documentObserver.observe(document, { childList: true, subtree: true });
    }
  });
};

const readThemeProbe = async (page: Page): Promise<ThemeProbe> => {
  const probe = await page.evaluate(() => {
    const serialized = document.documentElement.getAttribute('data-theme-contract-probe');
    if (!serialized) {
      throw new Error('Theme probe was not installed');
    }
    return JSON.parse(serialized) as ThemeProbe;
  });

  return probe;
};

const readThemeAtFirstFrame = async (page: Page): Promise<string | null> => page.evaluate(
  () => new Promise<string | null>((resolve) => {
    const capture = () => {
      const root = document.documentElement;
      if (!root) {
        requestAnimationFrame(capture);
        return;
      }
      resolve(root.getAttribute('data-theme'));
    };

    requestAnimationFrame(capture);
  }),
);

const findThemeControl = async (page: Page): Promise<ThemeControl> => {
  const dark = page.getByRole('button', { name: /^Темная тема$/i });
  const light = page.getByRole('button', { name: /^Светлая тема$/i });

  if ((await dark.count()) === 1 && (await light.count()) === 1) {
    return { kind: 'choices', dark, light };
  }

  const darkWithoutSuffix = page.getByRole('button', { name: /^Темная$/i });
  const lightWithoutSuffix = page.getByRole('button', { name: /^Светлая$/i });
  if ((await darkWithoutSuffix.count()) === 1 && (await lightWithoutSuffix.count()) === 1) {
    return { kind: 'choices', dark: darkWithoutSuffix, light: lightWithoutSuffix };
  }

  const namedButtonToggle = page.getByRole('button', { name: /тема|переключить/i });
  if ((await namedButtonToggle.count()) === 1) {
    return { kind: 'toggle', toggle: namedButtonToggle };
  }

  const namedSwitchToggle = page.getByRole('switch', { name: /тема|переключить/i });
  if ((await namedSwitchToggle.count()) === 1) {
    return { kind: 'toggle', toggle: namedSwitchToggle };
  }

  throw new Error('Russian theme control was not found');
};

const activateTheme = async (
  page: Page,
  control: ThemeControl,
  theme: Theme,
): Promise<Locator> => {
  const target = control.kind === 'choices' ? control[theme] : control.toggle;

  await target.focus();
  await expect(target).toBeFocused();
  await target.press('Enter');

  if (await page.locator('html').getAttribute('data-theme') !== theme) {
    await target.press('Space');
  }

  await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
  return target;
};

const readThemeState = async (page: Page) => page.evaluate(() => {
  const rootStyles = getComputedStyle(document.documentElement);
  const bodyStyles = getComputedStyle(document.body);
  const surfaceStyles = getComputedStyle(document.querySelector('.card') as HTMLElement);
  const accentTextStyles = getComputedStyle(document.querySelector('h2') as HTMLElement);
  const secondaryTextStyles = getComputedStyle(document.querySelector('p') as HTMLElement);

  return {
    tokens: {
      bg: rootStyles.getPropertyValue('--bg').trim(),
      surface: rootStyles.getPropertyValue('--surface').trim(),
      surfaceStrong: rootStyles.getPropertyValue('--surface-strong').trim(),
      text: rootStyles.getPropertyValue('--text').trim(),
      secondary: rootStyles.getPropertyValue('--secondary').trim(),
      subtle: rootStyles.getPropertyValue('--subtle').trim(),
      accent: rootStyles.getPropertyValue('--accent').trim(),
      accentStrong: rootStyles.getPropertyValue('--accent-strong').trim(),
      accentSoft: rootStyles.getPropertyValue('--accent-soft').trim(),
      success: rootStyles.getPropertyValue('--success').trim(),
      successSoft: rootStyles.getPropertyValue('--success-soft').trim(),
      error: rootStyles.getPropertyValue('--error').trim(),
      errorSoft: rootStyles.getPropertyValue('--error-soft').trim(),
    },
    colorScheme: rootStyles.colorScheme,
    bodyBackground: bodyStyles.backgroundColor,
    bodyColor: bodyStyles.color,
    surfaceBackground: surfaceStyles.backgroundColor,
    accentTextColor: accentTextStyles.color,
    secondaryTextColor: secondaryTextStyles.color,
    viewportWidth: window.innerWidth,
    documentScrollWidth: document.documentElement.scrollWidth,
    bodyScrollWidth: document.body.scrollWidth,
  };
});

const parseColor = (value: string): RGB | null => {
  const normalized = value.trim().toLowerCase();
  const hex = normalized.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/);
  if (hex) {
    const digits = hex[1];
    if (digits.length === 3) {
      return [
        Number.parseInt(`${digits[0]}${digits[0]}`, 16),
        Number.parseInt(`${digits[1]}${digits[1]}`, 16),
        Number.parseInt(`${digits[2]}${digits[2]}`, 16),
      ];
    }
    return [
      Number.parseInt(digits.slice(0, 2), 16),
      Number.parseInt(digits.slice(2, 4), 16),
      Number.parseInt(digits.slice(4, 6), 16),
    ];
  }

  const rgb = normalized.match(/^rgba?\(\s*([\d.]+)[, ]+\s*([\d.]+)[, ]+\s*([\d.]+)(?:\s*[,/]\s*([\d.]+))?\s*\)$/);
  if (!rgb || (rgb[4] !== undefined && Number.parseFloat(rgb[4]) === 0)) {
    return null;
  }

  return [
    Number.parseFloat(rgb[1]),
    Number.parseFloat(rgb[2]),
    Number.parseFloat(rgb[3]),
  ];
};

const relativeLuminance = ([red, green, blue]: RGB): number => {
  const linearize = (channel: number): number => {
    const normalized = channel / 255;
    return normalized <= 0.03928
      ? normalized / 12.92
      : ((normalized + 0.055) / 1.055) ** 2.4;
  };

  return (0.2126 * linearize(red))
    + (0.7152 * linearize(green))
    + (0.0722 * linearize(blue));
};

const contrastRatio = (foreground: RGB, background: RGB): number => {
  const foregroundLuminance = relativeLuminance(foreground);
  const backgroundLuminance = relativeLuminance(background);
  const lighter = Math.max(foregroundLuminance, backgroundLuminance);
  const darker = Math.min(foregroundLuminance, backgroundLuminance);
  return (lighter + 0.05) / (darker + 0.05);
};

const assertThemeVisualContract = async (page: Page, theme: Theme): Promise<void> => {
  const tokens = expectedTokens[theme];
  const expectedBackground = parseColor(tokens.bg);
  const expectedText = parseColor(tokens.text);
  if (!expectedBackground || !expectedText) {
    throw new Error('Expected theme colors could not be parsed');
  }

  await expect.poll(async () => {
    const state = await readThemeState(page);
    return {
      bodyBackground: parseColor(state.bodyBackground),
      bodyColor: parseColor(state.bodyColor),
      surfaceBackground: parseColor(state.surfaceBackground),
      accentTextColor: parseColor(state.accentTextColor),
      secondaryTextColor: parseColor(state.secondaryTextColor),
    };
  }).toEqual({
    bodyBackground: expectedBackground,
    bodyColor: expectedText,
    surfaceBackground: parseColor(tokens.surface),
    accentTextColor: parseColor(tokens.accent),
    secondaryTextColor: parseColor(tokens.secondary),
  });

  const state = await readThemeState(page);

  expect(state.tokens).toEqual({
    bg: tokens.bg.toLowerCase(),
    surface: tokens.surface.toLowerCase(),
    surfaceStrong: tokens.surfaceStrong.toLowerCase(),
    text: tokens.text.toLowerCase(),
    secondary: tokens.secondary.toLowerCase(),
    subtle: tokens.subtle.toLowerCase(),
    accent: tokens.accent.toLowerCase(),
    accentStrong: tokens.accentStrong.toLowerCase(),
    accentSoft: tokens.accentSoft.toLowerCase(),
    success: tokens.success.toLowerCase(),
    successSoft: tokens.successSoft.toLowerCase(),
    error: tokens.error.toLowerCase(),
    errorSoft: tokens.errorSoft.toLowerCase(),
  });
  expect(state.colorScheme).toBe(theme);

  const bodyBackground = parseColor(state.bodyBackground);
  const bodyColor = parseColor(state.bodyColor);
  const surfaceBackground = parseColor(state.surfaceBackground);
  const accentTextColor = parseColor(state.accentTextColor);
  const secondaryTextColor = parseColor(state.secondaryTextColor);
  if (
    !expectedBackground
    || !expectedText
    || !bodyBackground
    || !bodyColor
    || !surfaceBackground
    || !accentTextColor
    || !secondaryTextColor
  ) {
    throw new Error('Theme colors could not be parsed from the rendered page');
  }

  expect(bodyBackground).toEqual(expectedBackground);
  expect(bodyColor).toEqual(expectedText);
  expect(contrastRatio(expectedText, expectedBackground)).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(accentTextColor, surfaceBackground)).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(secondaryTextColor, surfaceBackground)).toBeGreaterThanOrEqual(4.5);
  const focusColor = parseColor(tokens.accentStrong);
  const focusSurface = parseColor(tokens.accentSoft);
  const subtleColor = parseColor(tokens.subtle);
  const strongSurface = parseColor(tokens.surfaceStrong);
  const successColor = parseColor(tokens.success);
  const successSurface = parseColor(tokens.successSoft);
  const errorColor = parseColor(tokens.error);
  const errorSurface = parseColor(tokens.errorSoft);
  if (
    !focusColor
    || !focusSurface
    || !subtleColor
    || !strongSurface
    || !successColor
    || !successSurface
    || !errorColor
    || !errorSurface
  ) {
    throw new Error('Theme status or focus colors could not be parsed from theme tokens');
  }
  expect(contrastRatio(focusColor, focusSurface)).toBeGreaterThanOrEqual(3);
  expect(contrastRatio(subtleColor, strongSurface)).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(successColor, successSurface)).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(errorColor, errorSurface)).toBeGreaterThanOrEqual(4.5);
  expect(state.documentScrollWidth).toBeLessThanOrEqual(state.viewportWidth);
  expect(state.bodyScrollWidth).toBeLessThanOrEqual(state.viewportWidth);
};

const assertThemeStorage = async (page: Page, theme: Theme): Promise<void> => {
  const entries = await page.evaluate(() => Object.entries(window.localStorage));
  expect(entries).toEqual([[themeStorageKey, theme]]);
};

const assertFocusIndicator = async (control: Locator): Promise<void> => {
  const state = await control.evaluate((element) => {
    const styles = getComputedStyle(element);
    const outlineVisible = styles.outlineStyle !== 'none'
      && Number.parseFloat(styles.outlineWidth) > 0
      && styles.outlineColor !== 'transparent'
      && styles.outlineColor !== 'rgba(0, 0, 0, 0)';
    const shadowVisible = styles.boxShadow !== 'none'
      && styles.boxShadow !== 'rgba(0, 0, 0, 0)';

    return {
      focusVisible: element.matches(':focus-visible'),
      outlineVisible,
      shadowVisible,
    };
  });

  expect(state.focusVisible).toBe(true);
  expect(state.outlineVisible || state.shadowVisible).toBe(true);
};

for (const width of viewportWidths) {
  test.describe(`theme contract at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } });

    test('defaults to dark, switches both themes by keyboard, and restores before DOMContentLoaded', async ({ page }) => {
      await installThemeProbe(page);
      await page.goto('/');

      const initialProbe = await readThemeProbe(page);
      expect(initialProbe.initialTheme).toBe('dark');
      expect(initialProbe.initialStorageKeys).toEqual([]);
      expect(initialProbe.domContentLoadedTheme).toBe('dark');
      expect(initialProbe.observedThemes.every((theme) => theme === 'dark')).toBe(true);
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
      await assertThemeVisualContract(page, 'dark');

      let control = await findThemeControl(page);
      const lightControl = await activateTheme(page, control, 'light');
      await assertFocusIndicator(lightControl);
      await assertThemeStorage(page, 'light');
      await assertThemeVisualContract(page, 'light');

      await page.reload({ waitUntil: 'commit' });
      expect(await readThemeAtFirstFrame(page)).toBe('light');
      await page.waitForLoadState('domcontentloaded');
      await page.waitForLoadState('load');
      const reloadProbe = await readThemeProbe(page);
      expect(reloadProbe.initialStorageKeys).toEqual([themeStorageKey]);
      expect(reloadProbe.domContentLoadedTheme).toBe('light');
      expect(reloadProbe.observedThemes[reloadProbe.observedThemes.length - 1]).toBe('light');
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
      await assertThemeVisualContract(page, 'light');

      control = await findThemeControl(page);
      const darkControl = await activateTheme(page, control, 'dark');
      await assertFocusIndicator(darkControl);
      await assertThemeStorage(page, 'dark');
      await assertThemeVisualContract(page, 'dark');
    });

    test('keeps keyboard theme switching working when browser storage throws SecurityError', async ({ page }) => {
      const pageErrors: string[] = [];
      page.on('pageerror', (error) => {
        pageErrors.push(error.message);
      });
      await page.addInitScript(() => {
        const throwSecurityError = () => {
          throw new DOMException('blocked', 'SecurityError');
        };

        Storage.prototype.getItem = throwSecurityError;
        Storage.prototype.setItem = throwSecurityError;
        Storage.prototype.removeItem = throwSecurityError;
      });

      await page.goto('/');
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

      let control = await findThemeControl(page);
      await activateTheme(page, control, 'light');
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

      control = await findThemeControl(page);
      await activateTheme(page, control, 'dark');
      await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
      expect(pageErrors).toEqual([]);
    });
  });
}
