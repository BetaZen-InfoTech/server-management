// PasskeyService — biometric gate for "Auto Passkey Login".
//
// Phase-1 reality: a true WebAuthn ceremony on Flutter needs either a
// WebView round-trip through the webmail SPA or a platform plugin
// (`webauthn_flutter`, currently iOS-only). To unblock the UX without
// shipping a half-finished WebAuthn, this service:
//
//   1. Uses local_auth to require a biometric/device-credential check
//      every time the operator opens the app with "auto passkey" on.
//   2. Persists a flag in SharedPreferences so AuthService can decide
//      whether to silently reuse the stored refresh token.
//
// Phase-2 will add a real WebAuthn ceremony backed by the backend's
// /api/v1/passkey/* endpoints (currently stubbed). The screens here
// already speak the right vocabulary so the swap is internal.

import 'package:flutter/foundation.dart';
import 'package:local_auth/local_auth.dart';
import 'package:shared_preferences/shared_preferences.dart';

class PasskeyService {
  PasskeyService({required SharedPreferences prefs}) : _prefs = prefs;

  final SharedPreferences _prefs;
  final LocalAuthentication _auth = LocalAuthentication();

  static const _kEnabled = 'passkey_auto_login';

  bool get enabled => _prefs.getBool(_kEnabled) ?? false;

  Future<void> setEnabled(bool v) async {
    await _prefs.setBool(_kEnabled, v);
  }

  Future<bool> available() async {
    try {
      return await _auth.canCheckBiometrics || await _auth.isDeviceSupported();
    } catch (_) {
      return false;
    }
  }

  Future<bool> authenticate({String reason = 'Unlock Betazen Mail'}) async {
    if (!enabled) return true; // No-op when feature off.
    try {
      return await _auth.authenticate(
        localizedReason: reason,
        options: const AuthenticationOptions(
          biometricOnly: false,
          stickyAuth: true,
        ),
      );
    } catch (e) {
      debugPrint('[passkey] local_auth failed: $e');
      return false;
    }
  }
}
