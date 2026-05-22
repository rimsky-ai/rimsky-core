import { expectedAttributesSchemaBytes, declaredEvents } from "./expected-attributes-schema.js";

export interface ObservabilityCapabilitiesResponse {
  expected_attributes_schema: Uint8Array;
  declared_events: string[];
  http_bridge_url: string;
}

export function buildCapabilitiesResponse(
  httpBridgeUrl?: string,
): ObservabilityCapabilitiesResponse {
  return {
    expected_attributes_schema: expectedAttributesSchemaBytes(),
    declared_events: declaredEvents,
    http_bridge_url: httpBridgeUrl ?? "",
  };
}
