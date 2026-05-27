# Salesforce Data Cloud vs Marketing Cloud: Everything You Need to Know

## 1. Salesforce Data Cloud (formerly called "Genie," now also branded as **Data 360**)

### What It Is
Salesforce **Data Cloud** is a **customer data platform (CDP)** that sits at the core of the Salesforce ecosystem. It's designed to **ingest, unify, harmonize, and activate** data from **any source** — not just Salesforce data — in **real time**.

### Key Capabilities
| Feature | Description |
|---|---|
| **Data Ingestion** | Connect to any source: Salesforce orgs, external databases, data lakes, APIs, streaming data, web/mobile SDKs |
| **Data Modeling** | Map external data to a standard data model (Individual, Account, etc.) using calculated insights and data model object (DMO) relationships |
| **Identity Resolution** | Stitch together fragmented customer profiles across systems into a **single unified profile** (golden record) |
| **Segmentation** | Build audiences/segments using calculated insights from harmonized data |
| **Activation** | Send audiences, insights, and calculated attributes to Marketing Cloud, Sales Cloud, Service Cloud, Advertising, etc. |
| **Real-Time Data Streams** | Process events like web visits, purchases, or service cases in real time |
| **AI & Einstein** | Feed unified data into Einstein AI for predictions, recommendations, and scoring |
| **Data Sharing (Zero-Copy)** | Access external data (Snowflake, Databricks, etc.) via zero-copy — no duplication needed |
| **Data Spaces** | Create isolated environments for different brands/business units |

### Why It Matters
- **Breaks down data silos** — unify CRM, web, mobile, advertising, support, and external data
- **Real-time unification** — identity resolution happens as data arrives
- **Powerful for Agentforce** — Data Cloud is the foundation for AI agents, providing a 360° view of every customer
- **Queryable via SQL** — data engineers can query Data Cloud objects using standard SQL

### Licensing Considerations
- Sold as separate subscription (not included with Marketing Cloud)
- Pricing based on **data storage** and **compute credits**
- Has a **free Developer Edition** org to learn

---

## 2. Salesforce Marketing Cloud

### What It Is
**Marketing Cloud** is Salesforce's **enterprise marketing automation platform**. It enables you to design, execute, and measure personalized multi-channel marketing campaigns.

### Editions
1. **Marketing Cloud Engagement** (the classic "Enterprise" MC, formerly ExactTarget)
2. **Marketing Cloud Account Engagement** (formerly Pardot — B2B focused)
3. **Marketing Cloud Growth Edition** (newer small-to-mid-market offering)

### Key Modules (Marketing Cloud Engagement)

| Module | Purpose |
|---|---|
| **Email Studio** | Drag-and-drop email creation, A/B testing, send management |
| **Mobile Studio** | SMS/MMS push notifications, in-app messaging |
| **Advertising Studio** | Integrate customer data with Facebook, Google, LinkedIn ads |
| **Journey Builder** | Visual, multi-step customer journeys across channels |
| **Automation Studio** | ETL, data imports, SQL-based segmentation, scheduled automations |
| **Audience Builder** | Basic segmentation and data filtering |
| **Content Builder** | Centralized content/asset management |
| **Interaction Studio** | Real-time web personalization and event tracking |
| **Datorama (Marketing Analytics)** | Marketing ROI analytics and reporting |
| **Einstein (AI)** | Send-time optimization, predictive scoring, content recommendations |
| **Social Studio** (legacy) | Social listening and publishing (now being de-emphasized) |

### Marketing Cloud Account Engagement (Pardot)
- **B2B focused** — lead scoring, email nurture, CRM integration
- Tied directly to Sales Cloud Opportunities
- Great for demand generation, drip campaigns, and prospect tracking

### Marketing Cloud Growth Edition
- Newer, lighter product (launched 2024)
- Built directly on the Salesforce **platform** (unlike Engagement which runs on a separate stack)
- Good for small-to-mid-sized businesses that want native Salesforce integration

---

## 3. How Data Cloud and Marketing Cloud Work Together

This is where the real power lies.

```
[External Data Sources] 
        ↓
  [Data Cloud] ←→ [Salesforce Core CRM] 
        ↓                    ↓
  [Unified Profiles]   [Sales/Service/Commerce]
        ↓
  [Marketing Cloud]  ←  receives audiences + insights
        ↓
  [Activation: Email, SMS, Ads, Journeys]
```

### The Integration
- **Data Cloud sends segments/audiences** to Marketing Cloud Engagement (via a connector)
- **Unified profiles** in Data Cloud include data Marketing Cloud never had before (e.g., web behavior, offline purchases, support cases)
- **Einstein AI** gets access to the unified data set, producing better predictions
- **Journey Builder** can use Data Cloud calculated insights as entry/exit criteria
- **Real-time triggers** from Data Cloud can start Marketing Cloud journeys

### Example Use Case
1. Data Cloud unifies: a customer's web browsing, loyalty card purchases, service cases, and ad clicks
2. Data Cloud calculates: "high churn risk" based on combined signals
3. Segment is sent to Marketing Cloud
4. Journey Builder fires a re-engagement email with a personalized offer
5. The offer is based on data that never existed in Marketing Cloud alone

---

## 4. Key Differences at a Glance

| Aspect | Data Cloud | Marketing Cloud |
|---|---|---|
| **Category** | Customer Data Platform (CDP) | Marketing Automation |
| **Primary Purpose** | Unify & harmonize all customer data across the enterprise | Execute multi-channel marketing campaigns |
| **Data Storage** | High-volume, scalable lake/pool architecture | Traditional relational database (Email/Mobile) |
| **Identity Resolution** | Advanced — fuzzy matching, deterministic, probabilistic | Basic — limited to subscriber key/sendable records |
| **Real-Time** | Built for streaming events | Near real-time (Journey Builder) |
| **Audience Creation** | Powerful SQL-based + Calculated Insights | Basic SQL (Automation Studio) + filters |
| **Activation Targets** | Marketing Cloud, Sales Cloud, Advertising, APIs | Email, SMS, Push, Ads, Web |
| **Pricing Model** | Storage + compute credits | License tiers + sends (email/SMS) |
| **Technical Complexity** | High — requires data engineering skills | Medium — more marketer-friendly |
| **Built On** | Hyperforce (Salesforce's new cloud infrastructure) | Legacy stack (MC Engagement) / Core (Growth) |

---

## 5. When to Use Which

### Use Data Cloud When You Need To:
- Unify data from 5+ systems into a 360° view
- Perform advanced identity resolution across disconnected data sources
- Feed unified data into AI models (Einstein, Agentforce)
- Create calculated insights (e.g., "lifetime value," "propensity to buy")
- Share data with Snowflake/Databricks via zero-copy
- Power AI agents (Agentforce) with rich customer context

### Use Marketing Cloud When You Need To:
- Send targeted email campaigns at scale
- Build automated multi-step journeys (welcome series, abandoned cart)
- Run SMS/MMS campaigns
- Manage B2B lead nurturing (Account Engagement)
- Measure marketing attribution and ROI
- Integrate ad campaigns with Facebook/Google/LinkedIn

### Use **Both** Together When You Want:
- The most personalized marketing campaigns powered by a truly unified data foundation
- AI-driven decisions based on the complete picture of the customer
- Real-time triggered communications from any event in the enterprise
- Agentforce (AI agents) to execute marketing tasks with full context

---

## 6. Architecture & Technical Notes

- **Marketing Cloud Engagement** runs on a **separate stack** from the main Salesforce platform (AWS-based). It has its own API (SOAP/REST), its own data model (Subscribers, Lists, Data Extensions), and its own scripting language (AMPscript, SSJS, GTL).
- **Data Cloud** is built on **Hyperforce** (Salesforce's infrastructure-as-code platform) and uses a **lakehouse architecture**.
- **The Connector** (Data Cloud → Marketing Cloud) uses an authenticated connector that syncs audiences and attributes automatically. It does **not** sync everything — you choose what to activate.
- **Marketing Cloud Growth Edition** is built directly on the Salesforce core platform, unlike Engagement — making it much easier to integrate with Data Cloud and other Salesforce clouds.

---

## 7. Certifications to Consider

| Certification | Focus |
|---|---|
| **Salesforce Data Cloud Consultant** | Data modeling, ingestion, identity, activation |
| **Marketing Cloud Administrator** | MC setup, subscriber management, sends |
| **Marketing Cloud Developer** | AMPscript, SSJS, APIs, automation |
| **Marketing Cloud Consultant** | Solution design, journey strategy |
| **Marketing Cloud Account Engagement Specialist** | Pardot (B2B) |
| **Marketing Cloud Email Specialist** | Email marketing best practices |

---

## 8. Current Trends (2024-2025)

- **Data Cloud is being renamed/rebranded as "Data 360"** in some of the newer Salesforce navigation
- **Agentforce** heavily depends on Data Cloud — AI agents need unified data to be useful
- **Zero-copy data sharing** with Snowflake and Databricks is a major differentiator
- **Marketing Cloud Growth Edition** is the future for SMB — Engagement remains for enterprise
- **Interaction Studio** capabilities are being folded into Data Cloud
- **Marketing Cloud Personalization** (formerly Interaction Studio) can now leverage Data Cloud segments natively
