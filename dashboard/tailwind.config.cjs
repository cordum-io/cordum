module.exports = {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: ["class", '[data-theme="dark"]'],
  theme: {
    extend: {
      fontFamily: {
        sans: ["\"IBM Plex Sans\"", "sans-serif"],
        display: ["\"Space Grotesk\"", "sans-serif"],
        mono: ["\"IBM Plex Mono\"", "monospace"],
      },
      colors: {
        base: "var(--bg)",
        surface: "var(--surface)",
        surface2: "var(--surface-2)",
        ink: "var(--text)",
        muted: "var(--muted)",
        accent: "var(--accent)",
        accent2: "var(--accent-2)",
        success: "var(--success)",
        warning: "var(--warning)",
        danger: "var(--danger)",
        border: "var(--border)",
      },
      borderRadius: {
        DEFAULT: "8px",
        sm: "6px",
        md: "8px",
        lg: "10px",
        xl: "12px",
      },
      boxShadow: {
        soft: "0 1px 2px var(--shadow), 0 2px 8px var(--shadow)",
        lift: "0 1px 3px var(--shadow), 0 4px 12px var(--shadow)",
        glow: "0 0 0 1px rgba(15, 127, 122, 0.12), 0 1px 3px rgba(15, 127, 122, 0.08)",
      },
    },
  },
  plugins: [],
};
