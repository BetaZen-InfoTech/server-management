import type { Config } from 'tailwindcss'

export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        brand: {
          50: '#eef2ff',
          100: '#e0e7ff',
          400: '#818cf8',
          500: '#6366f1',
          600: '#4f46e5',
          700: '#4338ca',
          800: '#3730a3',
        },
        ink: {
          50: '#f6f7fb',
          100: '#eceef5',
          200: '#dadeec',
          300: '#b9bfd5',
          500: '#6b7280',
          700: '#374151',
          800: '#1f2937',
          900: '#111827',
        },
      },
    },
  },
  darkMode: 'class',
  plugins: [],
} satisfies Config
