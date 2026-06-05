// Theme — matches the Betazen panel's blue accent so the mobile app
// looks like a sibling product, not a third-party client. Light + dark
// share the same seed so toggling system theme doesn't visually
// re-brand the app.

import 'package:flutter/material.dart';

const _seedColor = Color(0xFF2563EB); // tailwind blue-600, used in WHM

ThemeData buildLightTheme() {
  final scheme = ColorScheme.fromSeed(seedColor: _seedColor);
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    visualDensity: VisualDensity.adaptivePlatformDensity,
    appBarTheme: AppBarTheme(
      backgroundColor: scheme.surface,
      foregroundColor: scheme.onSurface,
      elevation: 0,
      centerTitle: false,
    ),
  );
}

ThemeData buildDarkTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: _seedColor,
    brightness: Brightness.dark,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    visualDensity: VisualDensity.adaptivePlatformDensity,
    appBarTheme: AppBarTheme(
      backgroundColor: scheme.surface,
      foregroundColor: scheme.onSurface,
      elevation: 0,
      centerTitle: false,
    ),
  );
}
