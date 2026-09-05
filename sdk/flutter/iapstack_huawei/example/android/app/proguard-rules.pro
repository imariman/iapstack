# Huawei's IAP integration uses reflection and device-provided HMS classes.
# Keep the SDK surface intact in release builds, as required by Huawei's
# obfuscation guidance.
-keepattributes *Annotation*,Exceptions,InnerClasses,Signature
-keep class com.hianalytics.android.** { *; }
-keep class com.huawei.hianalytics.** { *; }
-keep class com.huawei.updatesdk.** { *; }
-keep class com.huawei.hms.** { *; }

# These optional classes are supplied only by specific Huawei devices or HMS
# distributions. Suppress their absence without hiding unrelated R8 warnings.
-dontwarn com.huawei.android.os.BuildEx$VERSION
-dontwarn com.huawei.hianalytics.process.**
-dontwarn com.huawei.hianalytics.util.HiAnalyticTools
-dontwarn com.huawei.hms.iapfull.**
-dontwarn com.huawei.libcore.io.**
-dontwarn org.bouncycastle.crypto.BlockCipher
-dontwarn org.bouncycastle.crypto.engines.AESEngine
-dontwarn org.bouncycastle.crypto.prng.SP800SecureRandom
-dontwarn org.bouncycastle.crypto.prng.SP800SecureRandomBuilder
