package com.iapstack.huawei

import com.google.gson.JsonParser
import java.io.File
import kotlin.test.assertTrue

internal class CustomerIdVector(val name: String, val value: String, val accepted: Boolean)

/** Loads the shared cases; number segments are UTF-16 code units, so lone surrogates survive. */
internal fun customerIdVectors(): List<CustomerIdVector> {
  val file = File("../../../contracts/test-vectors/external-customer-ids.json")
  assertTrue(file.isFile, "The shared customer ID vectors must be available.")
  return JsonParser.parseString(file.readText()).asJsonObject["cases"].asJsonArray.map { element ->
    val case = element.asJsonObject
    val value = buildString {
      for (segment in case["value"].asJsonArray) {
        val primitive = segment.asJsonPrimitive
        if (primitive.isNumber) append(Char(primitive.asInt)) else append(primitive.asString)
      }
    }
    CustomerIdVector(case["name"].asString, value, case["accepted"].asBoolean)
  }
}
