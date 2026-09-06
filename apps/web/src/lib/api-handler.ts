import nookies from "nookies";
import { API } from "../config/api-config";

interface ApiHandlerOptions {
  context?: object;
  path?: string;
  params?: object;
  data?: object;
  headers?: object;
}

export class ApiHandler {
  context?: object;
  path?: string;
  params?: object;
  data?: object;
  headers?: object;

  constructor(object: ApiHandlerOptions) {
    this.context = object.context || undefined;
    this.path = object.path || "";
    this.params = object.params || {};
    this.data = object.data || {};
    this.headers = object.headers || {};
  }

  private fetchToken(): Promise<string> {
    return new Promise((resolve) => {
      let token: string | null = "";
      if (this.context) {
        // If context is provided to the handler, resolve SSR
        const cookies = nookies.get(this.context);
        token = cookies["access_token"];
      } else {
        // If no context exists, resolve CSR
        token = localStorage.getItem("access_token");
      }
      resolve(token ? `Bearer ${token}` : "");
    });
  }

  async get(url?: string) {
    try {
      /** If url is present in the method, this.path is overriden */
      const path = url ? url : this.path;
      const token = await this.fetchToken();
      const response = await API.get(path, {
        headers: {
          ...this.headers,
          Authorization: token,
        },
        params: this.params,
      });
      return response;
    } catch (error) {
      throw error;
    }
  }

  async post() {
    try {
      const token = await this.fetchToken();
      const response = await API.post(this.path, this.data, {
        headers: {
          ...this.headers,
          Authorization: token,
        },
      });
      return response;
    } catch (error) {
      throw error;
    }
  }

  async patch() {
    try {
      const token = await this.fetchToken();
      const response = await API.patch(this.path, this.data, {
        headers: {
          ...this.headers,
          Authorization: token,
        },
      });
      return response;
    } catch (error) {
      throw error;
    }
  }

  async put() {
    try {
      const token = await this.fetchToken();
      const response = await API.put(this.path, this.data, {
        headers: {
          ...this.headers,
          Authorization: token,
        },
      });
      return response;
    } catch (error) {
      throw error;
    }
  }

  async delete() {
    try {
      const token = await this.fetchToken();
      const response = await API.delete(this.path, {
        headers: {
          ...this.headers,
          Authorization: token,
        },
      });
      return response;
    } catch (error) {
      throw error;
    }
  }
}
