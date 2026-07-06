// Global navigator key so services without a BuildContext (e.g. FcmService
// handling a notification tap) can drive navigation.

import 'package:flutter/widgets.dart';

final GlobalKey<NavigatorState> rootNavigatorKey = GlobalKey<NavigatorState>();
