import { userdataSchemaBytes, declaredEvents } from "./userdata-schema.js";

export interface ObservabilityCapabilitiesResponse {
  userdata_schema: Uint8Array;
  declared_events: string[];
  http_bridge_url: string;
}

export function buildCapabilitiesResponse(
  httpBridgeUrl?: string,
): ObservabilityCapabilitiesResponse {
  return {
    userdata_schema: userdataSchemaBytes(),
    declared_events: declaredEvents,
    http_bridge_url: httpBridgeUrl ?? "",
  };
}
