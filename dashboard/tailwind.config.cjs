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
      boxShadow: {
        lift: "0 10px 30px rgba(20, 31, 35, 0.15)",
        glow: "0 0 0 1px rgba(15, 127, 122, 0.2), 0 15px 30px rgba(15, 127, 122, 0.15)",
      },
    },
  },
  plugins: [],
};
