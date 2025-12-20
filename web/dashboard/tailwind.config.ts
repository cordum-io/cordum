import type { Config } from "tailwindcss";

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      backgroundImage: {
        "gradient-radial": "radial-gradient(var(--tw-gradient-stops))",
      },
      colors: {
        "primary-text": "#d1d1f5",
        "secondary-text": "#b1b1f5",
        "tertiary-text": "#8c8cdf",

        primary: "#6666f5",
        "primary-hover": "#5555f5",

        "primary-contrast": "#ffffff",

        "secondary-primary": "#2a2a3a",
        "secondary-primary-hover": "#3a3a4a",

        "primary-background": "#14141a",
        "secondary-background": "#1a1a20",
        "tertiary-background": "#2a2a30",

        "primary-border": "#2a2a3a",
        "secondary-border": "#3a3a4a",
      },
    },
  },
  plugins: [],
} satisfies Config;
