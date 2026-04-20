module.exports = {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: ["class", '[data-theme="dark"]'],
  theme: {
    extend: {
      fontFamily: {
        sans: ["\"Plus Jakarta Sans\"", "\"Inter\"", "sans-serif"],
        display: ["\"Plus Jakarta Sans\"", "sans-serif"],
        mono: ["\"JetBrains Mono\"", "monospace"],
      },
      colors: {
        /* Legacy aliases — keep for backward compat */
        base: "var(--bg)",
        surface: "var(--surface)",
        surface1: "var(--surface-1)",
        surface2: "var(--surface-2)",
        surface4: "var(--surface-4)",
        ink: "var(--text)",
        muted: "var(--muted)",
        accent2: "var(--accent-2)",
        border: "var(--border)",

        /* Canonical design-system tokens */
        background: "var(--background)",
        "surface-0": "var(--surface-0)",
        "surface-1": "var(--surface-1)",
        "surface-2": "var(--surface-2)",
        "surface-3": "var(--surface-3)",
        "surface-4": "var(--surface-4)",
        foreground: "var(--foreground)",
        "secondary-foreground": "var(--secondary-foreground)",
        "muted-foreground": "var(--muted-foreground)",
        accent: "var(--accent)",
        "accent-dim": "var(--color-cordum-dim)",
        "accent-bright": "var(--color-cordum-bright)",
        success: "var(--success)",
        warning: "var(--warning)",
        danger: "var(--danger)",
        info: "var(--info)",
        input: "var(--input)",

        /* Status surface tokens */
        "status-success-bg": "var(--status-success-bg)",
        "status-success-border": "var(--status-success-border)",
        "status-warning-bg": "var(--status-warning-bg)",
        "status-warning-border": "var(--status-warning-border)",
        "status-danger-bg": "var(--status-danger-bg)",
        "status-danger-border": "var(--status-danger-border)",
        "status-info-bg": "var(--status-info-bg)",
        "status-info-border": "var(--status-info-border)",
        "status-muted-bg": "var(--status-muted-bg)",
        "status-muted-border": "var(--status-muted-border)",

        /* Chart tokens */
        "chart-1": "var(--chart-1)",
        "chart-2": "var(--chart-2)",
        "chart-3": "var(--chart-3)",
        "chart-4": "var(--chart-4)",
        "chart-5": "var(--chart-5)",
      },
      borderRadius: {
        sm: "var(--radius-sm)",
        md: "var(--radius-md)",
        lg: "var(--radius-lg)",
        xl: "var(--radius-xl)",
      },
      boxShadow: {
        lift: "0 10px 30px rgba(20, 31, 35, 0.15)",
      },
      transitionDuration: {
        micro: "var(--motion-micro)",
        page: "var(--motion-page)",
        enter: "var(--motion-enter)",
      },
    },
  },
  plugins: [],
};
