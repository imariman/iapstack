module.exports = {dependency: {platforms: {
  ios: {podspecPath: 'IAPStackReactNative.podspec'},
  android: {sourceDir: 'android', packageImportPath: 'import com.iapstack.reactnative.IAPStackPackage;', packageInstance: 'new IAPStackPackage()'},
}}};
