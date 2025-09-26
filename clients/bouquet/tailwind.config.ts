/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        // Custom colors that work in both themes
        brand: {
          pink: "#be185d",
          blue: "#2563eb",
          purple: "#7c3aed",
        },
        // Custom grays
        gray: {
          850: "#1f2937",
          950: "#0f172a",
        }
      },
    },
  },
  plugins: [require('daisyui')],
  daisyui: {
    themes: [
      {
        light: {
          primary: "#be185d", // Your custom pink
          secondary: "#2563eb", // Your custom blue
          accent: "#f59e0b", // Custom accent color
          neutral: "#f3f4f6",
          "neutral-focus": "#e5e7eb",
          "neutral-content": "#374151", // Lighter gray for better readability
          "base-100": "#ffffff",
          "base-200": "#f9fafb",
          "base-300": "#f3f4f6",
          "base-content": "#1f2937",
          info: "#3b82f6",
          success: "#10b981",
          warning: "#f59e0b",
          error: "#ef4444",
        },
      },
      {
        dark: {
          primary: "#be185d", // Same pink for consistency
          secondary: "#2563eb", // Same blue for consistency
          accent: "#fbbf24", // Lighter accent for dark mode
          neutral: "#1f2937",
          "neutral-focus": "#111827",
          "neutral-content": "#d1d5db",
          "base-100": "#111827",
          "base-200": "#0f172a",
          "base-300": "#0d1117",
          "base-content": "#f9fafb",
          info: "#60a5fa",
          success: "#34d399",
          warning: "#fbbf24",
          error: "#f87171",
        },
      },
    ],
    darkTheme: false, // disable automatic dark mode detection
    base: true, // applies background color and foreground color for root element by default
    styled: true, // include daisyUI colors and design decisions for all components
    utils: true, // adds responsive and modifier utility classes
    prefix: '', // prefix for daisyUI classnames (components, modifiers and responsive class names. Not colors)
    logs: true, //Shows info about daisyUI version and used config in the console when building your CSS
    themeRoot: ':root', // The element that receives theme color CSS variables
  },
};
