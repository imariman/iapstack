require 'json'
package = JSON.parse(File.read(File.join(__dir__, 'package.json')))
Pod::Spec.new do |s|
  s.name = 'IAPStackReactNative'
  s.version = package['version']
  s.summary = package['description']
  s.homepage = package['homepage']
  s.license = { :type => 'Apache-2.0', :file => 'LICENSE' }
  s.authors = package['author']
  s.source = { :http => "https://registry.npmjs.org/@iapstack/react-native/-/react-native-#{package['version']}.tgz" }
  s.platform = :ios, '15.1'
  s.swift_version = '5.0'
  s.source_files = 'ios/**/*.{h,m,mm,swift}', 'native/ios/**/*.swift'
  s.frameworks = 'StoreKit'
  s.dependency 'React-Core'
end
