package com.iapstack.core

import org.yaml.snakeyaml.Yaml
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class OpenApiContractTest {
  private val document: Map<String, Any?> = loadContract()

  @Test
  fun declaresEveryOperationUsedByTheProviderNeutralClient() {
    val paths = mapAt(document, "paths")
    assertEquals("verifyPurchase", operationId(paths, "/v1/applications/{application_id}/purchases:verify", "post"))
    assertEquals("restorePurchases", operationId(paths, "/v1/applications/{application_id}/purchases:restore", "post"))
    assertEquals(
      "getCustomerEntitlements",
      operationId(
        paths,
        "/v1/applications/{application_id}/customers/{external_customer_id}/entitlements",
        "get",
      ),
    )
  }

  @Test
  fun keepsRequestResponseAndErrorFieldsConsumedByTheSdk() {
    val schemas = mapAt(mapAt(document, "components"), "schemas")
    expectRequiredFields(schemas, "PurchaseSubmission", setOf("external_customer_id", "claimed_products", "evidence"))
    expectRequiredFields(schemas, "VerificationResult", setOf("verified_at", "customer_id", "entitlements"))
    expectRequiredFields(schemas, "RestoreResult", setOf("results"))
    expectRequiredFields(schemas, "EntitlementSnapshot", setOf("customer_id", "entitlements"))
    expectRequiredFields(schemas, "ErrorEnvelope", setOf("error"))
    expectRequiredFields(schemas, "ApiError", setOf("code", "message"))
  }

  private fun operationId(paths: Map<String, Any?>, path: String, method: String): String {
    val pathItem = mapAt(paths, path)
    val operation = mapAt(pathItem, method)
    return operation["operationId"] as String
  }

  private fun expectRequiredFields(schemas: Map<String, Any?>, schemaName: String, expected: Set<String>) {
    val schema = mapAt(schemas, schemaName)
    val properties = mapAt(schema, "properties").keys
    val required = (schema["required"] as List<*>).map { it as String }.toSet()
    assertTrue(properties.containsAll(expected), "$schemaName properties missing $expected")
    assertTrue(required.containsAll(expected), "$schemaName required missing $expected")
  }

  private fun mapAt(source: Map<String, Any?>, key: String): Map<String, Any?> {
    @Suppress("UNCHECKED_CAST")
    return source[key] as Map<String, Any?>
  }

  private fun loadContract(): Map<String, Any?> {
    val contract = File("../../../contracts/openapi/v1.yaml")
    assertTrue(contract.isFile, "The canonical repository contract must be available.")
    @Suppress("UNCHECKED_CAST")
    return Yaml().load<Map<String, Any?>>(contract.readText())
  }
}
