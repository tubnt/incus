import { http } from "./http";

export interface ShadowActingAs {
  target_user_id: number;
  target_email: string;
  actor_id: number;
  actor_email: string;
}

export interface User {
  id: number;
  email: string;
  name: string;
  role: "admin" | "customer";
  balance: number;
  /**
   * Present when the current browser session is acting as another user via
   * shadow-login. The banner + audit UI keys off this — id/email fields
   * above reflect the *target* (so business logic is correctly scoped),
   * while acting_as identifies the real admin behind the session.
   */
  acting_as?: ShadowActingAs;
}

export async function fetchCurrentUser(): Promise<User> {
  return http.get<User>("/auth/me");
}

export function isAdmin(user: User): boolean {
  return user.role === "admin";
}

export function isShadowing(user: User): boolean {
  return !!user.acting_as;
}
